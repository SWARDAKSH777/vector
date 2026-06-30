package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	ipinfoTokenConfigKey       = "ipinfo_token_enc"
	ipinfoEnvMigrationDoneKey  = "ipinfo_env_migration_v1"
	ipinfoTokenEncryptionScope = "ipinfo-lite-token:v1"
)

type ipinfoTokenStatus struct {
	HasToken    bool   `json:"has_token"`
	Configured  bool   `json:"configured"`
	Provider    string `json:"provider"`
	CountryOnly bool   `json:"country_only"`
}

func encryptIPInfoToken(masterKey []byte, token string) (string, error) {
	return encryptAEAD(masterKey, token, ipinfoTokenEncryptionScope)
}

func decryptIPInfoToken(masterKey []byte, encoded string) (string, error) {
	return decryptAEAD(masterKey, encoded, ipinfoTokenEncryptionScope)
}

// loadOrMigrateIPInfoToken loads the write-only encrypted database secret.
// Older releases accepted IPINFO_TOKEN from the service environment; that path
// is read at most once and migrated into encrypted storage so the UI can take
// over without silently losing an existing deployment token.
func loadOrMigrateIPInfoToken(db *sql.DB, masterKey []byte) (string, error) {
	var encoded string
	err := db.QueryRow(`SELECT value FROM config WHERE key=?`, ipinfoTokenConfigKey).Scan(&encoded)
	if err == nil {
		token, decErr := decryptIPInfoToken(masterKey, encoded)
		if decErr != nil {
			return "", fmt.Errorf("decrypt stored IPinfo token: %w", decErr)
		}
		if !validGeoToken(token) {
			return "", errors.New("stored IPinfo token is invalid")
		}
		return token, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("read stored IPinfo token: %w", err)
	}

	var migrationDone string
	migrationErr := db.QueryRow(`SELECT value FROM config WHERE key=?`, ipinfoEnvMigrationDoneKey).Scan(&migrationDone)
	if migrationErr != nil && migrationErr != sql.ErrNoRows {
		return "", fmt.Errorf("read IPinfo migration state: %w", migrationErr)
	}
	if migrationDone == "true" {
		return "", nil
	}

	legacyToken := strings.TrimSpace(osEnv("IPINFO_TOKEN"))
	tx, txErr := db.Begin()
	if txErr != nil {
		return "", fmt.Errorf("begin IPinfo token migration: %w", txErr)
	}
	defer func() { _ = tx.Rollback() }()
	if legacyToken != "" {
		if !validGeoToken(legacyToken) {
			return "", errors.New("legacy IPINFO_TOKEN is invalid; add a replacement in Settings")
		}
		encoded, encErr := encryptIPInfoToken(masterKey, legacyToken)
		if encErr != nil {
			return "", fmt.Errorf("encrypt legacy IPinfo token: %w", encErr)
		}
		if _, txErr = tx.Exec(`INSERT INTO config(key,value) VALUES(?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, ipinfoTokenConfigKey, encoded); txErr != nil {
			return "", fmt.Errorf("store migrated IPinfo token: %w", txErr)
		}
	}
	if _, txErr = tx.Exec(`INSERT INTO config(key,value) VALUES(?, 'true')
		ON CONFLICT(key) DO UPDATE SET value='true'`, ipinfoEnvMigrationDoneKey); txErr != nil {
		return "", fmt.Errorf("mark IPinfo token migration: %w", txErr)
	}
	if txErr = tx.Commit(); txErr != nil {
		return "", fmt.Errorf("commit IPinfo token migration: %w", txErr)
	}
	return legacyToken, nil
}

func (s *server) currentIPInfoTokenStatus() ipinfoTokenStatus {
	configured := s.geo != nil && s.geo.configured()
	return ipinfoTokenStatus{
		HasToken:    configured,
		Configured:  configured,
		Provider:    "IPinfo Lite",
		CountryOnly: true,
	}
}

func (s *server) handleGetIPInfoToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentIPInfoTokenStatus())
}

func (s *server) handleSaveIPInfoToken(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if !validGeoToken(token) {
		writeErr(w, http.StatusBadRequest, "token must be a non-empty printable value without spaces")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	if s.geo == nil {
		writeErr(w, http.StatusServiceUnavailable, "country lookup service is unavailable")
		return
	}
	if err := s.geo.validateToken(ctx, token); err != nil {
		s.audit(r, uid, "analytics.ipinfo_token_validation_failed", "settings", "ipinfo", map[string]any{"reason": sanitizeAuditReason(err.Error())})
		writeErr(w, http.StatusBadRequest, "IPinfo token could not be validated; check the token and try again")
		return
	}

	encoded, err := encryptIPInfoToken(s.masterKey, token)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not encrypt token")
		return
	}
	if _, err := s.db.Exec(`INSERT INTO config(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, ipinfoTokenConfigKey, encoded); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save token")
		return
	}
	s.geo.setToken(token)
	s.clearConfigCache()
	s.clearAnalyticsReportCache()
	s.audit(r, uid, "analytics.ipinfo_token_saved", "settings", "ipinfo", nil)
	writeJSON(w, http.StatusOK, s.currentIPInfoTokenStatus())
}

func (s *server) handleDeleteIPInfoToken(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	if _, err := s.db.Exec(`DELETE FROM config WHERE key=?`, ipinfoTokenConfigKey); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove token")
		return
	}
	if s.geo != nil {
		s.geo.setToken("")
	}
	s.clearConfigCache()
	s.clearAnalyticsReportCache()
	s.audit(r, uid, "analytics.ipinfo_token_deleted", "settings", "ipinfo", nil)
	writeJSON(w, http.StatusOK, s.currentIPInfoTokenStatus())
}

func sanitizeAuditReason(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
		if b.Len() >= 256 {
			break
		}
	}
	return b.String()
}
