package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type privacySettings struct {
	AnalyticsEnabled       bool `json:"analytics_enabled"`
	AnalyticsRetentionDays int  `json:"analytics_retention_days"`
	AuditRetentionDays     int  `json:"audit_retention_days"`
	HonorsGPCAndDNT        bool `json:"honors_gpc_and_dnt"`
	StoresRawIP            bool `json:"stores_raw_ip"`
}

func (s *server) currentPrivacySettings() privacySettings {
	return privacySettings{
		AnalyticsEnabled:       s.getConfig("analytics_enabled") == "true",
		AnalyticsRetentionDays: boundedConfigDays(s.getConfig("analytics_retention_days"), 90, 1, 3650),
		AuditRetentionDays:     boundedConfigDays(s.getConfig("audit_retention_days"), 365, 30, 3650),
		HonorsGPCAndDNT:        true, StoresRawIP: false,
	}
}
func (s *server) handleGetPrivacySettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentPrivacySettings())
}
func (s *server) handleUpdatePrivacySettings(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	var req struct {
		AnalyticsEnabled       *bool `json:"analytics_enabled"`
		AnalyticsRetentionDays *int  `json:"analytics_retention_days"`
		AuditRetentionDays     *int  `json:"audit_retention_days"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, 400, "invalid request body")
		return
	}
	if req.AnalyticsRetentionDays != nil && (*req.AnalyticsRetentionDays < 1 || *req.AnalyticsRetentionDays > 3650) {
		writeErr(w, 400, "analytics retention must be between 1 and 3650 days")
		return
	}
	if req.AuditRetentionDays != nil && (*req.AuditRetentionDays < 30 || *req.AuditRetentionDays > 3650) {
		writeErr(w, 400, "audit retention must be between 30 and 3650 days")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, 500, "could not update privacy settings")
		return
	}
	set := func(k, v string) {
		if err == nil {
			_, err = tx.Exec(`INSERT INTO config(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
		}
	}
	if req.AnalyticsEnabled != nil {
		set("analytics_enabled", strconv.FormatBool(*req.AnalyticsEnabled))
	}
	if req.AnalyticsRetentionDays != nil {
		set("analytics_retention_days", strconv.Itoa(*req.AnalyticsRetentionDays))
	}
	if req.AuditRetentionDays != nil {
		set("audit_retention_days", strconv.Itoa(*req.AuditRetentionDays))
	}
	if err != nil {
		_ = tx.Rollback()
		writeErr(w, 500, "could not update privacy settings")
		return
	}
	if err = tx.Commit(); err != nil {
		writeErr(w, 500, "could not update privacy settings")
		return
	}
	s.clearConfigCache()
	s.clearAnalyticsReportCache()
	s.audit(r, uid, "privacy.settings_updated", "settings", "privacy", nil)
	writeJSON(w, 200, s.currentPrivacySettings())
}
func (s *server) handleDeleteAnalytics(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	role, roleErr := s.userRole(uid)
	if roleErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not verify account permissions")
		return
	}
	isAdmin := role == "admin"
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, 500, "could not delete analytics")
		return
	}
	res, err := tx.Exec(`DELETE FROM analytics_events WHERE link_id IN (SELECT id FROM links WHERE user_id=?)`, uid)
	if err != nil {
		_ = tx.Rollback()
		writeErr(w, 500, "could not delete analytics")
		return
	}
	deleted, _ := res.RowsAffected()
	if _, err = tx.Exec(`DELETE FROM clicks WHERE link_id IN (SELECT id FROM links WHERE user_id=?)`, uid); err != nil {
		_ = tx.Rollback()
		writeErr(w, 500, "could not delete legacy analytics")
		return
	}
	if _, err = tx.Exec(`DELETE FROM click_rollups WHERE link_id IN (SELECT id FROM links WHERE user_id=?)`, uid); err != nil {
		_ = tx.Rollback()
		writeErr(w, 500, "could not delete aggregate analytics")
		return
	}
	if isAdmin {
		if _, err = tx.Exec(`DELETE FROM geo_country_cache`); err != nil {
			_ = tx.Rollback()
			writeErr(w, 500, "could not delete geographic cache")
			return
		}
	}
	res, err = tx.Exec(`UPDATE links SET click_count=0 WHERE user_id=?`, uid)
	if err != nil {
		_ = tx.Rollback()
		writeErr(w, 500, "could not reset link click counters")
		return
	}
	reset, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "could not delete analytics")
		return
	}
	if isAdmin && s.geo != nil {
		s.geo.reset()
	}
	s.clearAnalyticsReportCache()
	s.audit(r, uid, "privacy.analytics_deleted", "analytics", "all", map[string]any{
		"rows": deleted, "link_counters_reset": reset,
	})
	writeJSON(w, 200, map[string]any{"ok": true, "deleted": deleted, "counters_reset": reset, "geo_cache_reset": isAdmin})
}

func (s *server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	role, roleErr := s.userRole(uid)
	if roleErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not verify account permissions")
		return
	}
	limit := 100
	if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 && n <= 500 {
		limit = n
	}
	query := `SELECT id,event,target_type,target_id,metadata,created_at FROM audit_logs WHERE actor_id=? ORDER BY id DESC LIMIT ?`
	args := []any{uid, limit}
	if role == "admin" {
		query = `SELECT id,event,target_type,target_id,metadata,created_at FROM audit_logs ORDER BY id DESC LIMIT ?`
		args = []any{limit}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		writeErr(w, 500, "could not load audit log")
		return
	}
	defer rows.Close()
	type item struct {
		ID         int64           `json:"id"`
		Event      string          `json:"event"`
		TargetType string          `json:"target_type"`
		TargetID   string          `json:"target_id"`
		Metadata   json.RawMessage `json:"metadata"`
		CreatedAt  time.Time       `json:"created_at"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		var raw string
		if err := rows.Scan(&it.ID, &it.Event, &it.TargetType, &it.TargetID, &raw, &it.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read audit log")
			return
		}
		if !json.Valid([]byte(raw)) {
			raw = `{}`
		}
		it.Metadata = json.RawMessage(raw)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load audit log")
		return
	}
	writeJSON(w, 200, out)
}

func (s *server) handleDataExport(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	type exportDomain struct {
		Hostname   string `json:"hostname"`
		Status     string `json:"status"`
		IsDefault  bool   `json:"is_default"`
		AccessRole string `json:"access_role"`
		OwnerEmail string `json:"owner_email"`
	}
	type exportLink struct {
		ShortCode      string    `json:"short_code"`
		DestinationURL string    `json:"destination_url"`
		Domain         string    `json:"domain"`
		RedirectType   string    `json:"redirect_type"`
		Tag            string    `json:"tag"`
		Notes          string    `json:"notes"`
		Status         string    `json:"status"`
		ClickCount     int64     `json:"click_count"`
		CreatedAt      time.Time `json:"created_at"`
	}
	export := struct {
		ExportedAt   time.Time       `json:"exported_at"`
		AccountEmail string          `json:"account_email"`
		Privacy      privacySettings `json:"privacy"`
		Domains      []exportDomain  `json:"domains"`
		Links        []exportLink    `json:"links"`
	}{ExportedAt: time.Now().UTC(), Privacy: s.currentPrivacySettings(), Domains: []exportDomain{}, Links: []exportLink{}}
	if err := s.db.QueryRow(`SELECT email FROM users WHERE id=?`, uid).Scan(&export.AccountEmail); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not export account data")
		return
	}
	rows, err := s.db.Query(`SELECT d.hostname,d.status,dm.is_default,dm.access_role,owner.email FROM domain_members dm JOIN domains d ON d.id=dm.domain_id JOIN users owner ON owner.id=d.user_id WHERE dm.user_id=? ORDER BY d.created_at`, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not export domain data")
		return
	}
	for rows.Next() {
		var d exportDomain
		if err := rows.Scan(&d.Hostname, &d.Status, &d.IsDefault, &d.AccessRole, &d.OwnerEmail); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, "could not read domain export data")
			return
		}
		export.Domains = append(export.Domains, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeErr(w, http.StatusInternalServerError, "could not export domain data")
		return
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT short_code,destination_url,domain,redirect_type,COALESCE(tag,''),COALESCE(notes,''),status,click_count,created_at FROM links WHERE user_id=? ORDER BY id`, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not export link data")
		return
	}
	for rows.Next() {
		var l exportLink
		if err := rows.Scan(&l.ShortCode, &l.DestinationURL, &l.Domain, &l.RedirectType, &l.Tag, &l.Notes, &l.Status, &l.ClickCount, &l.CreatedAt); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, "could not read link export data")
			return
		}
		export.Links = append(export.Links, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeErr(w, http.StatusInternalServerError, "could not export link data")
		return
	}
	rows.Close()
	w.Header().Set("Content-Disposition", `attachment; filename="vector-data-export.json"`)
	s.audit(r, uid, "privacy.data_exported", "user", idString(uid), nil)
	writeJSON(w, 200, export)
}

var _ = sql.ErrNoRows
