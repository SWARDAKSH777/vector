package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"urlshortener/sqlite3local"
)

const (
	maxDestinationURLBytes = 4096
	maxAliasBytes          = 128
	maxTagBytes            = 128
	maxNotesBytes          = 4096
	maxUTMBytes            = 256
	maxClicksAllowed       = int64(1_000_000_000)
)

func validateDestURL(raw string) error {
	if raw == "" || len(raw) > maxDestinationURLBytes || !utf8.ValidString(raw) {
		return &simpleErr{"destination URL is invalid or too long"}
	}
	if strings.ContainsAny(raw, "\r\n\x00") {
		return &simpleErr{"destination URL contains invalid characters"}
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || !parsed.IsAbs() {
		return &simpleErr{"invalid URL format"}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return &simpleErr{"only http:// and https:// destination URLs are allowed"}
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return &simpleErr{"URL must include a host"}
	}
	if parsed.User != nil {
		return &simpleErr{"URLs containing embedded usernames or passwords are not allowed"}
	}
	return nil
}

func validateAlias(alias string, subdomain bool) error {
	if alias == "" || len(alias) > maxAliasBytes {
		return &simpleErr{"alias must be between 1 and 128 characters"}
	}
	if reservedCodes[strings.ToLower(alias)] {
		return &simpleErr{"that alias is reserved, please choose another"}
	}
	if subdomain {
		return validateSubdomainLabel(strings.ToLower(alias))
	}
	for _, c := range alias {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return &simpleErr{"alias may contain only letters, numbers, hyphens and underscores"}
		}
	}
	return nil
}

func validateOptionalText(value string, max int, field string) error {
	if len(value) > max || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return &simpleErr{field + " is invalid or too long"}
	}
	return nil
}

type createLinkRequest struct {
	DestinationURL string `json:"destination_url"`
	CustomAlias    string `json:"custom_alias"`
	Domain         string `json:"domain"`
	RedirectType   string `json:"redirect_type"` // "slug" (default) | "subdomain"
	Tag            string `json:"tag"`
	Notes          string `json:"notes"`
	Password       string `json:"password"`
	ExpiresAt      string `json:"expires_at"` // date or RFC3339
	ExpiresIn      string `json:"expires_in"` // "1h","24h","7d","30d" — shorthand
	MaxClicks      *int64 `json:"max_clicks"`
	UTMSource      string `json:"utm_source"`
	UTMMedium      string `json:"utm_medium"`
	UTMCampaign    string `json:"utm_campaign"`
}

func (s *server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	s.linkMutationMu.Lock()
	defer s.linkMutationMu.Unlock()

	uid := userIDFromCtx(r.Context())
	var req createLinkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dest := strings.TrimSpace(req.DestinationURL)
	if err := validateDestURL(dest); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// A slice is intentional. A map keyed by the input value collapses fields
	// that happen to contain identical text and can apply the wrong size limit.
	for _, rule := range []struct {
		value string
		max   int
		field string
	}{
		{req.Tag, maxTagBytes, "tag"},
		{req.Notes, maxNotesBytes, "notes"},
		{req.UTMSource, maxUTMBytes, "UTM source"},
		{req.UTMMedium, maxUTMBytes, "UTM medium"},
		{req.UTMCampaign, maxUTMBytes, "UTM campaign"},
	} {
		if err := validateOptionalText(rule.value, rule.max, rule.field); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateLinkPassword(req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.MaxClicks != nil && (*req.MaxClicks < 1 || *req.MaxClicks > maxClicksAllowed) {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("max_clicks must be between 1 and %d", maxClicksAllowed))
		return
	}
	if req.ExpiresIn != "" && req.ExpiresAt != "" {
		writeErr(w, http.StatusBadRequest, "provide only one of expires_in or expires_at")
		return
	}

	redirectType := "slug"
	if req.RedirectType == "subdomain" {
		redirectType = "subdomain"
	} else if req.RedirectType != "" && req.RedirectType != "slug" {
		writeErr(w, http.StatusBadRequest, "redirect_type must be slug or subdomain")
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		var err error
		domain, err = s.defaultDomainForUserE(uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not determine the default domain")
			return
		}
	}
	if domain == "" {
		writeErr(w, http.StatusConflict, "no active default domain is configured")
		return
	}
	var domainID int64
	var domainStatus string
	if err := s.db.QueryRow(`SELECT d.id,d.status FROM domains d JOIN domain_members dm ON dm.domain_id=d.id WHERE dm.user_id=? AND d.hostname=?`, uid, domain).Scan(&domainID, &domainStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "selected domain is not available to this account")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not validate the selected domain")
		}
		return
	}
	if domainStatus != "active" {
		writeErr(w, http.StatusConflict, "selected domain is not active; verify it before creating links")
		return
	}

	code := strings.TrimSpace(req.CustomAlias)
	var err error
	if code == "" {
		code, err = generateUniqueCode(s.db, domain, redirectType)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		if redirectType == "subdomain" {
			code = strings.ToLower(code)
		}
		if err := validateAlias(code, redirectType == "subdomain"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		var exists int
		if err := s.db.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM links
			WHERE domain=? COLLATE NOCASE
			  AND redirect_type=?
			  AND short_code=? COLLATE BINARY
		)`, domain, redirectType, code).Scan(&exists); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not validate alias availability")
			return
		} else if exists > 0 {
			writeErr(w, http.StatusConflict, "that alias is already taken on the selected domain")
			return
		}
	}

	var passwordHash any
	if req.Password != "" {
		hash, hashErr := hashPasswordWithError(req.Password)
		if hashErr != nil {
			writeErr(w, http.StatusInternalServerError, "secure password hashing is temporarily unavailable")
			return
		}
		passwordHash = hash
	}
	var expiresAt any
	if req.ExpiresIn != "" {
		dur := parseDurationShorthand(req.ExpiresIn)
		if dur <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid expires_in value")
			return
		}
		expiresAt = time.Now().UTC().Add(dur)
	} else if req.ExpiresAt != "" {
		t, err := parseFlexibleDate(req.ExpiresAt)
		if err != nil || !t.After(time.Now()) {
			writeErr(w, http.StatusBadRequest, "expires_at must be a future date")
			return
		}
		expiresAt = t
	}

	var dnsProvision *subdomainDNSProvision
	if redirectType == "subdomain" {
		dnsProvision, err = s.provisionSubdomainDNS(uid, domainID, code, domain)
		if err != nil {
			var provisionErr *linkProvisionError
			if errors.As(err, &provisionErr) {
				writeErr(w, provisionErr.status, provisionErr.msg)
			} else {
				writeErr(w, http.StatusInternalServerError, "could not provision subdomain DNS")
			}
			return
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		if dnsProvision != nil {
			_ = dnsProvision.client.deleteRecordByID(dnsProvision.zoneID, dnsProvision.recordID)
		}
		writeErr(w, 500, "could not create link")
		return
	}
	res, err := tx.Exec(`INSERT INTO links
		(user_id,short_code,destination_url,domain,redirect_type,tag,notes,password_hash,expires_at,max_clicks,utm_source,utm_medium,utm_campaign)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, uid, code, dest, domain, redirectType, req.Tag, req.Notes, passwordHash,
		expiresAt, req.MaxClicks, req.UTMSource, req.UTMMedium, req.UTMCampaign)
	if err != nil {
		_ = tx.Rollback()
		if dnsProvision != nil {
			_ = dnsProvision.client.deleteRecordByID(dnsProvision.zoneID, dnsProvision.recordID)
		}
		if sqlite3local.IsConstraint(err) {
			writeErr(w, http.StatusConflict, "that alias is already taken on the selected domain")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not create link")
		}
		return
	}
	id, err := res.LastInsertId()
	if err == nil && dnsProvision != nil {
		_, err = tx.Exec(`INSERT INTO managed_dns_records(link_id,domain_id,zone_id,record_id,hostname) VALUES(?,?,?,?,?)`,
			id, domainID, dnsProvision.zoneID, dnsProvision.recordID, code+"."+domain)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		if dnsProvision != nil {
			_ = dnsProvision.client.deleteRecordByID(dnsProvision.zoneID, dnsProvision.recordID)
		}
		writeErr(w, http.StatusInternalServerError, "could not finalize link creation")
		return
	}
	s.audit(r, uid, "link.created", "link", idString(id), map[string]any{"type": redirectType, "domain": domain})
	s.clearAnalyticsReportCache()
	link, err := s.fetchLink(uid, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "link created but could not be loaded")
		return
	}
	writeJSON(w, http.StatusCreated, link)
}

func validateSubdomainLabel(label string) error {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return &simpleErr{"subdomain prefix must be 1-63 characters and cannot start or end with a hyphen"}
	}
	for _, c := range label {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return &simpleErr{"subdomain prefix may contain only lowercase letters, numbers and hyphens"}
		}
	}
	return nil
}

type subdomainDNSProvision struct {
	client   *cloudflareClient
	zoneID   string
	recordID string
}

type linkProvisionError struct {
	status int
	msg    string
}

func (e *linkProvisionError) Error() string { return e.msg }

func newLinkProvisionError(status int, msg string) error {
	return &linkProvisionError{status: status, msg: msg}
}

func (s *server) provisionSubdomainDNS(uid, domainID int64, subdomain, domain string) (*subdomainDNSProvision, error) {
	var status, tokenEnc string
	if err := s.db.QueryRow(`SELECT d.status,COALESCE(d.cloudflare_token_enc,'') FROM domains d
		JOIN domain_members dm ON dm.domain_id=d.id
		WHERE d.id=? AND dm.user_id=? AND d.hostname=?`, domainID, uid, domain).Scan(&status, &tokenEnc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, newLinkProvisionError(http.StatusNotFound, "selected domain was not found")
		}
		return nil, newLinkProvisionError(http.StatusInternalServerError, "could not load the selected domain")
	}
	if status != "active" {
		return nil, newLinkProvisionError(http.StatusConflict, "selected domain is not active")
	}
	if tokenEnc == "" {
		return nil, newLinkProvisionError(http.StatusConflict, "subdomain links require a valid Cloudflare API token for the selected domain")
	}
	token, err := s.decryptDomainToken(domain, tokenEnc)
	if err != nil || token == "" {
		s.setDomainError(uid, domainID, "Stored Cloudflare token could not be decrypted. Replace the token.")
		return nil, newLinkProvisionError(http.StatusConflict, "stored Cloudflare token is invalid; replace it in Domains")
	}

	targetHost, err := s.getConfigE("domain")
	if err != nil {
		return nil, newLinkProvisionError(http.StatusInternalServerError, "could not load the primary origin domain")
	}
	if strings.TrimSpace(targetHost) == "" {
		return nil, newLinkProvisionError(http.StatusConflict, "primary origin domain is not configured")
	}
	cf := &cloudflareClient{apiToken: token, targetHost: targetHost, http: defaultHTTPClient()}
	zone, err := cf.findZoneForHostname(domain) // always live: catches revoked or out-of-scope tokens
	if err != nil {
		message := "Cloudflare API validation failed: " + err.Error()
		s.setDomainError(uid, domainID, message)
		return nil, newLinkProvisionError(http.StatusBadGateway, message+". No link was created.")
	}
	if _, err := cf.listZoneRecords(zone.ID); err != nil {
		message := "Cloudflare DNS permission check failed: " + err.Error()
		s.setDomainError(uid, domainID, message)
		return nil, newLinkProvisionError(http.StatusBadGateway, message+". No link was created.")
	}

	hostname := subdomain + "." + domain
	record, err := cf.createSubdomainRecord(zone.ID, hostname)
	if err != nil {
		message := "could not create Cloudflare DNS record for " + hostname + ": " + err.Error()
		// Existing-record conflicts do not mean the token itself is unhealthy.
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			s.setDomainError(uid, domainID, "Cloudflare DNS creation failed: "+err.Error())
		}
		statusCode := http.StatusBadGateway
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			statusCode = http.StatusConflict
		}
		return nil, newLinkProvisionError(statusCode, message+". No link was created.")
	}
	if _, err := s.db.Exec(`UPDATE domains SET cloudflare_zone_id=?, status='active' WHERE id=?`, zone.ID, domainID); err != nil {
		// Do not leave an untracked DNS record when local ownership state cannot be
		// persisted. A later delete would otherwise be unable to clean it up.
		_ = cf.deleteRecordByID(zone.ID, record.ID)
		return nil, newLinkProvisionError(http.StatusInternalServerError, "could not save Cloudflare DNS ownership; no link was created")
	}
	return &subdomainDNSProvision{client: cf, zoneID: zone.ID, recordID: record.ID}, nil
}

func (s *server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := r.URL.Query().Get("status")
	if len(q) > 512 || !utf8.ValidString(q) || strings.ContainsRune(q, '\x00') {
		writeErr(w, http.StatusBadRequest, "search query is invalid or too long")
		return
	}
	if status != "" && status != "all" && status != "active" && status != "paused" && status != "expired" {
		writeErr(w, http.StatusBadRequest, "invalid link status filter")
		return
	}

	query := `SELECT id, short_code, destination_url, domain, redirect_type, tag, notes,
		password_hash, expires_at, max_clicks, utm_source, utm_medium, utm_campaign,
		status, click_count, created_at FROM links WHERE user_id = ?`
	args := []any{uid}

	if q != "" {
		query += ` AND (destination_url LIKE ? ESCAPE '\' OR short_code LIKE ? ESCAPE '\' OR tag LIKE ? ESCAPE '\' OR notes LIKE ? ESCAPE '\')`
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		like := "%" + escaped + "%"
		args = append(args, like, like, like, like)
	}
	if status != "" && status != "all" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	limit := 200
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 && parsed <= 500 {
		limit = parsed
	}
	offset := 0
	if parsed, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && parsed >= 0 && parsed <= 1_000_000 {
		offset = parsed
	}
	query += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list links")
		return
	}
	defer rows.Close()

	links := []Link{}
	for rows.Next() {
		l, err := scanLink(rows, s.getPublicBaseURL())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read link data")
			return
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list links")
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *server) handleGetLink(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	link, err := s.fetchLink(uid, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "link not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load link")
		return
	}
	writeJSON(w, http.StatusOK, link)
}

type updateLinkRequest struct {
	DestinationURL *string `json:"destination_url"`
	Tag            *string `json:"tag"`
	Notes          *string `json:"notes"`
	Status         *string `json:"status"`
	Password       *string `json:"password"`
	ClearPassword  bool    `json:"clear_password"`
	ExpiresAt      *string `json:"expires_at"`
	ExpiresIn      *string `json:"expires_in"`
	ClearExpiry    bool    `json:"clear_expiry"`
	MaxClicks      *int64  `json:"max_clicks"`
	ClearMaxClicks bool    `json:"clear_max_clicks"`
}

func (s *server) handleUpdateLink(w http.ResponseWriter, r *http.Request) {
	s.linkMutationMu.Lock()
	defer s.linkMutationMu.Unlock()

	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	var req updateLinkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DestinationURL != nil {
		trimmed := strings.TrimSpace(*req.DestinationURL)
		req.DestinationURL = &trimmed
		if err := validateDestURL(trimmed); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Tag != nil {
		if err := validateOptionalText(*req.Tag, maxTagBytes, "tag"); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if req.Notes != nil {
		if err := validateOptionalText(*req.Notes, maxNotesBytes, "notes"); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "paused" {
		writeErr(w, 400, "status must be active or paused")
		return
	}
	if req.Password != nil && *req.Password != "" {
		if err := validateLinkPassword(*req.Password); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if req.MaxClicks != nil && (*req.MaxClicks < 1 || *req.MaxClicks > maxClicksAllowed) {
		writeErr(w, 400, "invalid max_clicks")
		return
	}
	if req.ClearPassword && req.Password != nil && *req.Password != "" {
		writeErr(w, http.StatusBadRequest, "clear_password cannot be combined with password")
		return
	}
	if req.ClearExpiry && ((req.ExpiresIn != nil && *req.ExpiresIn != "") || (req.ExpiresAt != nil && *req.ExpiresAt != "")) {
		writeErr(w, http.StatusBadRequest, "clear_expiry cannot be combined with an expiry value")
		return
	}
	if req.ExpiresIn != nil && *req.ExpiresIn != "" && req.ExpiresAt != nil && *req.ExpiresAt != "" {
		writeErr(w, http.StatusBadRequest, "provide only one of expires_in or expires_at")
		return
	}
	if req.ClearMaxClicks && req.MaxClicks != nil {
		writeErr(w, http.StatusBadRequest, "clear_max_clicks cannot be combined with max_clicks")
		return
	}

	// Parse all mutable availability controls before opening the transaction,
	// then evaluate the resulting state. This prevents an "active" response for
	// a link whose old expiry or click cap already makes it unusable.
	var parsedExpiry *time.Time
	if req.ExpiresIn != nil && *req.ExpiresIn != "" {
		dur := parseDurationShorthand(*req.ExpiresIn)
		if dur <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid expires_in value")
			return
		}
		value := time.Now().UTC().Add(dur)
		parsedExpiry = &value
	} else if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		value, parseErr := parseFlexibleDate(*req.ExpiresAt)
		if parseErr != nil || !value.After(time.Now()) {
			writeErr(w, http.StatusBadRequest, "expires_at must be a future date")
			return
		}
		parsedExpiry = &value
	}

	var currentStatus string
	var currentExpiry sql.NullTime
	var currentMax sql.NullInt64
	var lifetimeClicks int64
	if err := s.db.QueryRow(`SELECT status,expires_at,max_clicks,lifetime_click_count
		FROM links WHERE id=? AND user_id=?`, id, uid).
		Scan(&currentStatus, &currentExpiry, &currentMax, &lifetimeClicks); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "link not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load link state")
		return
	}
	effectiveStatus := currentStatus
	if req.Status != nil {
		effectiveStatus = *req.Status
	}
	effectiveExpiry := currentExpiry
	if req.ClearExpiry {
		effectiveExpiry = sql.NullTime{}
	} else if parsedExpiry != nil {
		effectiveExpiry = sql.NullTime{Time: *parsedExpiry, Valid: true}
	}
	effectiveMax := currentMax
	if req.ClearMaxClicks {
		effectiveMax = sql.NullInt64{}
	} else if req.MaxClicks != nil {
		effectiveMax = sql.NullInt64{Int64: *req.MaxClicks, Valid: true}
	}
	reactivateAfterClearingMax := req.Status == nil &&
		req.ClearMaxClicks &&
		currentStatus == "paused" &&
		currentMax.Valid &&
		currentMax.Int64 > 0 &&
		lifetimeClicks >= currentMax.Int64
	if reactivateAfterClearingMax {
		effectiveStatus = "active"
	}

	if effectiveStatus == "active" {
		if effectiveExpiry.Valid && !effectiveExpiry.Time.After(time.Now()) {
			writeErr(w, http.StatusConflict, "clear or extend the expired date before activating this link")
			return
		}
		if effectiveMax.Valid && lifetimeClicks >= effectiveMax.Int64 {
			writeErr(w, http.StatusConflict, "clear or increase the exhausted click limit before activating this link")
			return
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, 500, "could not update link")
		return
	}
	exec := func(query string, args ...any) bool {
		if err != nil {
			return false
		}
		_, err = tx.Exec(query, args...)
		return err == nil
	}
	if req.DestinationURL != nil {
		exec(`UPDATE links SET destination_url=? WHERE id=? AND user_id=?`, *req.DestinationURL, id, uid)
	}
	if req.Tag != nil {
		exec(`UPDATE links SET tag=? WHERE id=? AND user_id=?`, *req.Tag, id, uid)
	}
	if req.Notes != nil {
		exec(`UPDATE links SET notes=? WHERE id=? AND user_id=?`, *req.Notes, id, uid)
	}
	if req.Status != nil {
		exec(`UPDATE links SET status=? WHERE id=? AND user_id=?`, *req.Status, id, uid)
	} else if reactivateAfterClearingMax {
		exec(`UPDATE links SET status='active' WHERE id=? AND user_id=?`, id, uid)
	}
	if req.ClearPassword {
		exec(`UPDATE links SET password_hash=NULL WHERE id=? AND user_id=?`, id, uid)
	} else if req.Password != nil && *req.Password != "" {
		passwordHash, hashErr := hashPasswordWithError(*req.Password)
		if hashErr != nil {
			_ = tx.Rollback()
			writeErr(w, http.StatusInternalServerError, "secure password hashing is temporarily unavailable")
			return
		}
		exec(`UPDATE links SET password_hash=? WHERE id=? AND user_id=?`, passwordHash, id, uid)
	}
	if req.ClearExpiry {
		exec(`UPDATE links SET expires_at=NULL WHERE id=? AND user_id=?`, id, uid)
	} else if parsedExpiry != nil {
		exec(`UPDATE links SET expires_at=? WHERE id=? AND user_id=?`, *parsedExpiry, id, uid)
	}
	if req.ClearMaxClicks {
		exec(`UPDATE links SET max_clicks=NULL WHERE id=? AND user_id=?`, id, uid)
	} else if req.MaxClicks != nil {
		exec(`UPDATE links SET max_clicks=? WHERE id=? AND user_id=?`, *req.MaxClicks, id, uid)
	}
	if err != nil {
		_ = tx.Rollback()
		writeErr(w, 500, "could not update link")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "could not update link")
		return
	}
	s.audit(r, uid, "link.updated", "link", idString(id), nil)
	s.clearAnalyticsReportCache()
	link, err := s.fetchLink(uid, id)
	if err != nil {
		writeErr(w, 500, "could not reload link")
		return
	}
	writeJSON(w, http.StatusOK, link)
}

func (s *server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	s.linkMutationMu.Lock()
	defer s.linkMutationMu.Unlock()

	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	var domain, tokenEnc, zoneID, recordID, previousStatus string
	err := s.db.QueryRow(`SELECT l.domain, l.status, COALESCE(d.cloudflare_token_enc,''), COALESCE(m.zone_id,''), COALESCE(m.record_id,'')
		FROM links l LEFT JOIN domains d ON d.hostname=l.domain COLLATE NOCASE
		LEFT JOIN managed_dns_records m ON m.link_id=l.id
		WHERE l.id=? AND l.user_id=?`, id, uid).Scan(&domain, &previousStatus, &tokenEnc, &zoneID, &recordID)
	if err == sql.ErrNoRows {
		writeErr(w, http.StatusNotFound, "link not found")
		return
	}
	if err != nil {
		writeErr(w, 500, "could not load link")
		return
	}
	if recordID != "" {
		// Pause first so a local delete failure after successful DNS cleanup can
		// never leave an apparently active subdomain whose DNS record is gone.
		if _, err := s.db.Exec(`UPDATE links SET status='paused' WHERE id=? AND user_id=?`, id, uid); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not safely prepare link deletion")
			return
		}
		token, decErr := s.decryptDomainToken(domain, tokenEnc)
		if decErr != nil || token == "" {
			_, _ = s.db.Exec(`UPDATE links SET status=? WHERE id=? AND user_id=?`, previousStatus, id, uid)
			writeErr(w, http.StatusConflict, "cannot safely remove the managed DNS record until the domain token is restored")
			return
		}
		cf := &cloudflareClient{apiToken: token, http: defaultHTTPClient()}
		if err := cf.deleteRecordByID(zoneID, recordID); err != nil {
			_, _ = s.db.Exec(`UPDATE links SET status=? WHERE id=? AND user_id=?`, previousStatus, id, uid)
			writeErr(w, http.StatusBadGateway, "Cloudflare DNS cleanup failed; the link was not deleted: "+err.Error())
			return
		}
	}
	res, err := s.db.Exec(`DELETE FROM links WHERE id=? AND user_id=?`, id, uid)
	if err != nil {
		if recordID != "" {
			writeErr(w, http.StatusInternalServerError, "DNS was removed but local deletion failed; the link was paused. Retry deletion.")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not delete link")
		}
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not confirm link deletion")
		return
	}
	if n == 0 {
		writeErr(w, 404, "link not found")
		return
	}
	s.audit(r, uid, "link.deleted", "link", idString(id), nil)
	s.clearAnalyticsReportCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Helpers ----

func (s *server) fetchLink(uid, id int64) (Link, error) {
	row := s.db.QueryRow(`SELECT id, short_code, destination_url, domain, redirect_type, tag, notes,
		password_hash, expires_at, max_clicks, utm_source, utm_medium, utm_campaign,
		status, click_count, created_at FROM links WHERE id=? AND user_id=?`, id, uid)
	return scanLink(row, s.getPublicBaseURL())
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLink(row rowScanner, baseURL string) (Link, error) {
	var l Link
	var domain, redirectType, tag, notes, utmS, utmM, utmC sql.NullString
	var passwordHash sql.NullString
	var expiresAt sql.NullTime
	var maxClicks sql.NullInt64

	err := row.Scan(&l.ID, &l.ShortCode, &l.DestinationURL, &domain, &redirectType, &tag, &notes,
		&passwordHash, &expiresAt, &maxClicks, &utmS, &utmM, &utmC, &l.Status, &l.ClickCount, &l.CreatedAt)
	if err != nil {
		return Link{}, err
	}
	l.Domain = domain.String
	l.RedirectType = redirectType.String
	if l.RedirectType == "" {
		l.RedirectType = "slug"
	}
	l.Tag = tag.String
	l.Notes = notes.String
	l.UTMSource = utmS.String
	l.UTMMedium = utmM.String
	l.UTMCampaign = utmC.String
	l.HasPassword = passwordHash.Valid && passwordHash.String != ""
	if expiresAt.Valid {
		l.ExpiresAt = &expiresAt.Time
	}
	if maxClicks.Valid {
		l.MaxClicks = &maxClicks.Int64
	}

	base := l.Domain
	if base == "" {
		base = baseURL
	} else if !strings.HasPrefix(base, "http") {
		base = "https://" + base
	}

	if l.RedirectType == "subdomain" {
		// Short URL is subdomain.domain
		l.ShortURL = "https://" + l.ShortCode + "." + l.Domain
	} else {
		l.ShortURL = strings.TrimRight(base, "/") + "/" + l.ShortCode
	}
	return l, nil
}

func primaryDomainLabel(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	return u.Host
}

func parseFlexibleDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, &simpleErr{"unrecognized date format"}
}

func parseDurationShorthand(s string) time.Duration {
	switch s {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	return 0
}
