package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"urlshortener/sqlite3local"
)

const (
	deploymentModeSingle = "single"
	deploymentModeMulti  = "multi"
)

func normalizeDeploymentMode(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case deploymentModeSingle:
		return deploymentModeSingle, true
	case deploymentModeMulti:
		return deploymentModeMulti, true
	default:
		return "", false
	}
}

func (s *server) deploymentModeE() (string, error) {
	value, err := s.getConfigE("deployment_mode")
	if err != nil {
		return "", err
	}
	if mode, ok := normalizeDeploymentMode(value); ok {
		return mode, nil
	}
	return deploymentModeSingle, nil
}

func (s *server) isMultiUserE() (bool, error) {
	mode, err := s.deploymentModeE()
	return mode == deploymentModeMulti, err
}

func (s *server) userRole(uid int64) (string, error) {
	var role string
	var disabled int
	err := s.db.QueryRow(`SELECT role,disabled FROM users WHERE id=?`, uid).Scan(&role, &disabled)
	if err != nil {
		return "", err
	}
	if disabled != 0 {
		return "", sql.ErrNoRows
	}
	return role, nil
}

// requireSystemAdmin protects installation-wide settings. It applies in both
// deployment modes; requireAdmin below additionally hides the user-management
// API when the installation is configured as single-user.
func (s *server) requireSystemAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := userIDFromCtx(r.Context())
		role, err := s.userRole(uid)
		if errors.Is(err, sql.ErrNoRows) || role != "admin" {
			writeErr(w, http.StatusForbidden, "administrator access required")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not verify administrator access")
			return
		}
		next(w, r)
	}
}

func (s *server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		multi, err := s.isMultiUserE()
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "deployment mode is temporarily unavailable")
			return
		}
		if !multi {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		s.requireSystemAdmin(next)(w, r)
	}
}

type domainAccess struct {
	DomainID   int64
	OwnerID    int64
	Hostname   string
	Status     string
	AccessRole string
}

func (s *server) domainAccessByID(uid, domainID int64) (domainAccess, error) {
	var a domainAccess
	err := s.db.QueryRow(`SELECT d.id,d.user_id,d.hostname,d.status,dm.access_role
		FROM domains d JOIN domain_members dm ON dm.domain_id=d.id
		WHERE d.id=? AND dm.user_id=?`, domainID, uid).
		Scan(&a.DomainID, &a.OwnerID, &a.Hostname, &a.Status, &a.AccessRole)
	return a, err
}

func (s *server) domainAccessByHostname(uid int64, hostname string) (domainAccess, error) {
	var a domainAccess
	err := s.db.QueryRow(`SELECT d.id,d.user_id,d.hostname,d.status,dm.access_role
		FROM domains d JOIN domain_members dm ON dm.domain_id=d.id
		WHERE dm.user_id=? AND d.hostname=? COLLATE NOCASE`, uid, hostname).
		Scan(&a.DomainID, &a.OwnerID, &a.Hostname, &a.Status, &a.AccessRole)
	return a, err
}

func (s *server) ownedDomainByID(uid, domainID int64) (domainAccess, error) {
	var a domainAccess
	err := s.db.QueryRow(`SELECT id,user_id,hostname,status,'owner' FROM domains WHERE id=? AND user_id=?`, domainID, uid).
		Scan(&a.DomainID, &a.OwnerID, &a.Hostname, &a.Status, &a.AccessRole)
	return a, err
}

func repairDefaultDomainTx(tx *sql.Tx, uid int64) error {
	var exists int
	if err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM domain_members dm JOIN domains d ON d.id=dm.domain_id
		WHERE dm.user_id=? AND dm.is_default=1 AND d.status='active')`, uid).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return syncLegacyDefaultTx(tx, uid)
	}
	if _, err := tx.Exec(`UPDATE domain_members SET is_default=0 WHERE user_id=?`, uid); err != nil {
		return err
	}
	var domainID int64
	err := tx.QueryRow(`SELECT d.id FROM domain_members dm JOIN domains d ON d.id=dm.domain_id
		WHERE dm.user_id=? AND d.status='active'
		ORDER BY CASE dm.access_role WHEN 'owner' THEN 0 ELSE 1 END, d.created_at, d.id LIMIT 1`, uid).Scan(&domainID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if _, err := tx.Exec(`UPDATE domain_members SET is_default=1 WHERE user_id=? AND domain_id=?`, uid, domainID); err != nil {
			return err
		}
	}
	return syncLegacyDefaultTx(tx, uid)
}

func syncLegacyDefaultTx(tx *sql.Tx, uid int64) error {
	_, err := tx.Exec(`UPDATE domains SET is_default=CASE WHEN EXISTS(
		SELECT 1 FROM domain_members dm WHERE dm.domain_id=domains.id AND dm.user_id=?
		AND dm.access_role='owner' AND dm.is_default=1) THEN 1 ELSE 0 END WHERE user_id=?`, uid, uid)
	return err
}

type DomainMember struct {
	UserID     int64     `json:"user_id"`
	Email      string    `json:"email"`
	AccessRole string    `json:"access_role"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *server) handleListDomainMembers(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	domainID := atoiOr(r.PathValue("id"), -1)
	if _, err := s.ownedDomainByID(uid, domainID); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}
	rows, err := s.db.Query(`SELECT u.id,u.email,dm.access_role,dm.created_at
		FROM domain_members dm JOIN users u ON u.id=dm.user_id
		WHERE dm.domain_id=? ORDER BY CASE dm.access_role WHEN 'owner' THEN 0 ELSE 1 END,u.email`, domainID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list domain access")
		return
	}
	defer rows.Close()
	out := []DomainMember{}
	for rows.Next() {
		var m DomainMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.AccessRole, &m.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read domain access")
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list domain access")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAddDomainMember(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	domainID := atoiOr(r.PathValue("id"), -1)
	if _, err := s.ownedDomainByID(uid, domainID); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var memberID int64
	var disabled int
	if err := s.db.QueryRow(`SELECT id,disabled FROM users WHERE email=? COLLATE NOCASE`, email).Scan(&memberID, &disabled); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no active user exists with that email")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not find user")
		return
	}
	if disabled != 0 {
		writeErr(w, http.StatusConflict, "that user is deactivated")
		return
	}
	if memberID == uid {
		writeErr(w, http.StatusConflict, "the domain owner already has access")
		return
	}
	res, err := s.db.Exec(`INSERT INTO domain_members(domain_id,user_id,access_role,is_default) VALUES(?,?,'member',0)`, domainID, memberID)
	if err != nil {
		if sqlite3local.IsConstraint(err) {
			writeErr(w, http.StatusConflict, "that user already has access")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not share domain")
		}
		return
	}
	if _, err := res.RowsAffected(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not confirm domain access")
		return
	}
	s.audit(r, uid, "domain.member_added", "domain", idString(domainID), map[string]any{"member_id": memberID})
	writeJSON(w, http.StatusCreated, DomainMember{UserID: memberID, Email: email, AccessRole: "member", CreatedAt: time.Now().UTC()})
}

func (s *server) handleDeleteDomainMember(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	domainID := atoiOr(r.PathValue("id"), -1)
	memberID := atoiOr(r.PathValue("userID"), -1)
	if _, err := s.ownedDomainByID(uid, domainID); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}
	if memberID == uid {
		writeErr(w, http.StatusConflict, "domain ownership cannot be removed")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove domain access")
		return
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM domain_members WHERE domain_id=? AND user_id=? AND access_role='member'`, domainID, memberID)
	if err == nil {
		var affected int64
		affected, err = res.RowsAffected()
		if err == nil && affected == 0 {
			writeErr(w, http.StatusNotFound, "domain member not found")
			return
		}
	}
	if err == nil {
		err = repairDefaultDomainTx(tx, memberID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove domain access")
		return
	}
	s.audit(r, uid, "domain.member_removed", "domain", idString(domainID), map[string]any{"member_id": memberID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
