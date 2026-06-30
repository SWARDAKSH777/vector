package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func (s *server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	setupValue, err := s.getConfigE("setup_complete")
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "setup state is temporarily unavailable")
		return
	}
	domain, err := s.getConfigE("domain")
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "setup state is temporarily unavailable")
		return
	}
	done := setupValue == "true"
	bootstrapRequired, bootstrapAuthenticated, bootstrapAvailable, bootstrapMessage := bootstrapStatus(s, r)
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_complete":          done,
		"domain":                  domain,
		"bootstrap_required":      bootstrapRequired,
		"bootstrap_authenticated": bootstrapAuthenticated,
		"bootstrap_available":     bootstrapAvailable,
		"bootstrap_message":       bootstrapMessage,
	})
}

// handleSetupCheckDomain validates setup DNS without requiring an HTTP ownership challenge.
func (s *server) handleSetupCheckDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain          string `json:"domain"`
		CloudflareToken string `json:"cloudflare_token"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	token, err := normalizeCloudflareToken(req.CloudflareToken)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Domain) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "a domain is required to complete setup"})
		return
	}
	domain, err := normalizeHostname(req.Domain)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	method, err := checkSetupDomain(domain, token)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "method": method})
}

// checkSetupDomain deliberately avoids the HTTP ownership challenge. Before
// nginx is configured, the domain commonly reaches nginx's default vhost and
// returns 404. A Cloudflare token gives stronger proof through the CF API; the
// no-token fallback only checks that DNS and port 80 are reachable.
func checkSetupDomain(domain, cfToken string) (string, error) {
	if cfToken == "" {
		if err := checkDomainReachable(domain); err != nil {
			return "", err
		}
		return "http_reachability", nil
	}

	cf := &cloudflareClient{apiToken: cfToken, http: defaultHTTPClient()}
	zone, err := cf.findZoneForHostname(domain)
	if err != nil {
		return "", err
	}
	records, err := cf.listZoneRecords(zone.ID)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if strings.EqualFold(record.Name, domain) &&
			(record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME") {
			return "cloudflare_api", nil
		}
	}
	// A valid scoped token and accessible zone are sufficient at this stage.
	// The setup submit path creates or validates the actual DNS record before
	// saving the domain, so a fresh hostname must not be blocked merely because
	// its record does not exist yet.
	return "cloudflare_api_pending_dns", nil
}

func (s *server) handleSetupSubmit(w http.ResponseWriter, r *http.Request) {
	// Bootstrap requests can be retried by browsers and reverse proxies. Keep
	// account/domain creation single-flight so concurrent valid submissions do
	// not race to replace the administrator or create multiple defaults.
	s.setupSubmitMu.Lock()
	defer s.setupSubmitMu.Unlock()

	setupValue, configErr := s.getConfigE("setup_complete")
	if configErr != nil {
		writeErr(w, http.StatusServiceUnavailable, "setup state is temporarily unavailable")
		return
	}
	if setupValue == "true" {
		writeErr(w, http.StatusForbidden, "setup already completed")
		return
	}
	var req struct {
		Domain          string `json:"domain"`
		AdminEmail      string `json:"admin_email"`
		AdminPassword   string `json:"admin_password"`
		CloudflareToken string `json:"cloudflare_token"` // optional — stored encrypted for the domain
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Domain = strings.TrimSpace(req.Domain)
	if req.Domain == "" {
		writeErr(w, http.StatusBadRequest, "a domain is required to complete setup")
		return
	}
	email, err := normalizeEmail(req.AdminEmail)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.AdminEmail = email
	if err := validateAdminPassword(req.AdminPassword); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfToken, err := normalizeCloudflareToken(req.CloudflareToken)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Re-check on submit to prevent bypassing the UI validation. Cloudflare
	// tokens are verified through the CF API; no HTTP challenge is used here.
	domain, err := normalizeHostname(req.Domain)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Domain = domain
	if _, err := checkSetupDomain(req.Domain, cfToken); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	hash, err := hashPasswordWithError(req.AdminPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "secure password hashing is temporarily unavailable")
		return
	}
	var adminID int64
	userErr := s.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&adminID)
	if userErr == sql.ErrNoRows {
		res, insertErr := s.db.Exec(`INSERT INTO users (email, password_hash, password_changed_at, role, disabled) VALUES (?, ?, CURRENT_TIMESTAMP, 'admin', 0)`, req.AdminEmail, hash)
		if insertErr != nil {
			writeErr(w, http.StatusInternalServerError, "could not create admin user")
			return
		}
		adminID, insertErr = res.LastInsertId()
		if insertErr != nil {
			writeErr(w, http.StatusInternalServerError, "could not read admin user id")
			return
		}
	} else if userErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not inspect admin user")
		return
	} else if _, updateErr := s.db.Exec(`UPDATE users SET email=?, password_hash=?, password_changed_at=CURRENT_TIMESTAMP, role='admin', disabled=0 WHERE id=?`, req.AdminEmail, hash, adminID); updateErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not update admin user")
		return
	}

	if err := s.setConfigE("domain", req.Domain); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the configured domain")
		return
	}
	s.setPublicBaseURL("https://" + req.Domain)

	// Always create the setup domain row and make it the default. A token is
	// optional for slug links, but required for automatic subdomain DNS.
	if cfToken != "" {
		if err := s.storeSetupDomainToken(req.Domain, cfToken, setupPublicIP(r)); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not securely store Cloudflare token: "+err.Error())
			return
		}
	} else {
		tx, txErr := s.db.Begin()
		if txErr == nil {
			_, txErr = tx.Exec(`UPDATE domains SET is_default=0 WHERE user_id=?`, adminID)
		}
		if txErr == nil {
			_, txErr = tx.Exec(`INSERT INTO domains (user_id, hostname, status, message, is_default)
				VALUES (?,?,'pending','Complete nginx and SSL setup, then add a Cloudflare token to enable subdomain links.',1)
				ON CONFLICT(hostname) DO UPDATE SET user_id=excluded.user_id, status='pending',
				message=excluded.message, is_default=1`, adminID, req.Domain)
		}
		if txErr == nil {
			txErr = tx.Commit()
		} else if tx != nil {
			_ = tx.Rollback()
		}
		if txErr != nil {
			writeErr(w, http.StatusInternalServerError, "could not create setup domain")
			return
		}
	}
	// Establish the setup session in the same response that creates the account.
	// This avoids a fragile login-then-provision sequence during direct HTTP/IP
	// setup and uses a new cookie name so stale Secure cookies from older builds
	// cannot block the browser from storing the setup session.
	if err := s.createSession(w, r, adminID, true); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create setup session")
		return
	}

	// Keep the wizard resumable until nginx + TLS succeeds. Marking setup as
	// complete without a domain would make the API reject the direct-IP origin
	// and permanently lock the administrator out.
	if err := s.setConfigE("setup_complete", "false"); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save setup state")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "domain": req.Domain})
}

// storeSetupDomainToken creates a domain row for the setup domain and stores the CF token.
// Called once during setup — the user can manage it from the Domains page afterwards.
func (s *server) storeSetupDomainToken(hostname, token, primaryOriginIP string) error {
	enc, err := s.encryptDomainToken(hostname, token)
	if err != nil {
		return err
	}

	// Find existing user id (admin, id=1)
	var uid int64
	if err := s.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid); err != nil {
		return fmt.Errorf("admin user not found: %w", err)
	}

	// Upsert the setup domain and its default flag atomically. A retry with a
	// different hostname must not violate the single-default invariant or leave
	// two rows claiming to be the default after a partial write.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE domains SET is_default=0 WHERE user_id=?`, uid); err != nil {
		return err
	}
	var domainID, ownerID int64
	err = tx.QueryRow(`SELECT id,user_id FROM domains WHERE hostname=? COLLATE NOCASE`, hostname).Scan(&domainID, &ownerID)
	if err == sql.ErrNoRows {
		res, execErr := tx.Exec(
			`INSERT INTO domains (user_id, hostname, status, message, cloudflare_token_enc, is_default) VALUES (?,?,'pending','Cloudflare DNS configured; nginx and TLS setup is pending.',?,1)`,
			uid, hostname, enc)
		if execErr != nil {
			return execErr
		}
		domainID, err = res.LastInsertId()
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if ownerID != uid {
			return fmt.Errorf("domain is already owned by another account")
		}
		if _, err = tx.Exec(`UPDATE domains SET cloudflare_token_enc=?, status='pending', message='Cloudflare DNS configured; nginx and TLS setup is pending.', is_default=1 WHERE id=? AND user_id=?`, enc, domainID, uid); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}

	return s.resolveDomainWithCF(domainID, hostname, token, primaryOriginIP)
}

func setupPublicIP(r *http.Request) string {
	candidates := []string{stripHostPort(r.Host)}
	if trustedProxyRequest(r) {
		if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			candidates = append([]string{stripHostPort(strings.Split(forwardedHost, ",")[0])}, candidates...)
		}
	}
	// A root-managed installer value is the safe fallback when setup is reached
	// through an SSH tunnel or a private address. Never trust a public client's
	// forwarded headers for this value.
	if configured := strings.TrimSpace(os.Getenv("VECTOR_PUBLIC_IP")); configured != "" {
		candidates = append(candidates, configured)
	}
	for _, candidate := range candidates {
		ip := net.ParseIP(strings.TrimSpace(candidate))
		if ip != nil && isPublicDestinationIP(ip) {
			return ip.String()
		}
	}
	return ""
}

func (s *server) handleSetupNginx(w http.ResponseWriter, r *http.Request) {
	// DNS-01 issuance includes propagation waits and can legitimately exceed
	// the server-wide 45-second write timeout. Extend only this privileged setup
	// response rather than weakening timeouts for every public request.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(12 * time.Minute))

	var req struct {
		Domain string `json:"domain"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	domain, err := normalizeHostname(req.Domain)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	configured, configErr := s.getConfigE("domain")
	if configErr != nil {
		writeErr(w, http.StatusServiceUnavailable, "setup state is temporarily unavailable")
		return
	}
	if configured == "" || !strings.EqualFold(configured, domain) {
		writeErr(w, http.StatusBadRequest, "domain does not match the configured setup domain")
		return
	}
	port := getenv("INTERNAL_PORT", "8081")
	logStr, err := provisionDomain(s, domain, port)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"log":   logStr + fmt.Sprintf("\n✗ %s", err.Error()),
			"error": err.Error(),
		})
		return
	}
	tx, txErr := s.db.Begin()
	if txErr != nil {
		writeErr(w, http.StatusInternalServerError, "nginx and SSL succeeded, but setup state could not be saved")
		return
	}
	if _, txErr = tx.Exec(`INSERT INTO config(key,value) VALUES('domain',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, domain); txErr == nil {
		_, txErr = tx.Exec(`INSERT INTO config(key,value) VALUES('setup_complete','true') ON CONFLICT(key) DO UPDATE SET value='true'`)
	}
	if txErr == nil {
		_, txErr = tx.Exec(`UPDATE domains SET status='active', is_default=1,
			message='Domain configured during setup; nginx and SSL are active.' WHERE hostname=?`, domain)
	}
	if txErr != nil {
		_ = tx.Rollback()
		writeErr(w, http.StatusInternalServerError, "nginx and SSL succeeded, but setup state could not be saved; retry this step")
		return
	}
	if txErr = tx.Commit(); txErr != nil {
		writeErr(w, http.StatusInternalServerError, "nginx and SSL succeeded, but setup state could not be committed; retry this step")
		return
	}
	// The setup state is read through a short-lived in-memory cache. Clear it
	// before replying so the first HTTPS request cannot be sent back to setup.
	s.clearConfigCache()
	s.setPublicBaseURL("https://" + domain)
	s.consumeBootstrap(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": logStr, "url": "https://" + domain})
}

// ---- Alias reservation ----

type reservation struct {
	sessionID string
	expiresAt time.Time
}

const maxAliasReservations = 4096

var (
	aliasReservMu     sync.Mutex
	aliasReservations = make(map[string]*reservation)
)

func cleanupExpiredReservationsLocked(now time.Time) {
	for key, rv := range aliasReservations {
		if !now.Before(rv.expiresAt) {
			delete(aliasReservations, key)
		}
	}
}

func aliasReservationKey(uid int64, domain, redirectType, alias string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", uid, strings.ToLower(domain), redirectType, alias)
}

func (s *server) handleCheckAlias(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	alias := strings.TrimSpace(r.URL.Query().Get("alias"))
	sid := strings.TrimSpace(r.URL.Query().Get("sid"))
	if len(alias) > 128 || len(sid) > 128 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "invalid", "message": "Alias reservation input is too long"})
		return
	}
	redirectType := strings.TrimSpace(r.URL.Query().Get("redirect_type"))
	if redirectType == "" {
		redirectType = "slug"
	}
	if redirectType != "slug" && redirectType != "subdomain" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "invalid", "message": "Invalid redirect type"})
		return
	}

	domain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
	if domain == "" {
		var err error
		domain, err = s.defaultDomainForUserE(uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not determine the default domain")
			return
		}
	}
	if domain == "" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "invalid", "message": "Choose an active domain first"})
		return
	}
	var status string
	err := s.db.QueryRow(`SELECT status FROM domains WHERE user_id=? AND hostname=? COLLATE NOCASE`, uid, domain).Scan(&status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, "could not check the selected domain")
		return
	}
	if errors.Is(err, sql.ErrNoRows) || status != "active" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "invalid", "message": "Selected domain is not active"})
		return
	}

	if alias == "" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "empty"})
		return
	}
	if redirectType == "subdomain" {
		alias = strings.ToLower(alias)
	}
	if err := validateAlias(alias, redirectType == "subdomain"); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "invalid", "message": err.Error()})
		return
	}

	var exists int
	if err := s.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM links
		WHERE domain=? COLLATE NOCASE
		  AND redirect_type=?
		  AND short_code=? COLLATE BINARY
	)`, domain, redirectType, alias).Scan(&exists); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not check alias availability")
		return
	}
	if exists > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "taken", "message": "Alias already used on this domain"})
		return
	}

	now := time.Now()
	key := aliasReservationKey(uid, domain, redirectType, alias)
	aliasReservMu.Lock()
	defer aliasReservMu.Unlock()
	cleanupExpiredReservationsLocked(now)
	if rv, ok := aliasReservations[key]; ok && now.Before(rv.expiresAt) && rv.sessionID != sid {
		writeJSON(w, http.StatusOK, map[string]any{"status": "taken", "message": "Someone else is using this alias on this domain right now"})
		return
	}
	if sid != "" {
		if len(aliasReservations) >= maxAliasReservations {
			var oldestKey string
			var oldest time.Time
			for candidate, item := range aliasReservations {
				if oldestKey == "" || item.expiresAt.Before(oldest) {
					oldestKey, oldest = candidate, item.expiresAt
				}
			}
			delete(aliasReservations, oldestKey)
		}
		aliasReservations[key] = &reservation{sessionID: sid, expiresAt: now.Add(30 * time.Second)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "available"})
}

// ---- Per-domain CF token ----

func (s *server) handleDomainTokenSave(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	token, err := normalizeCloudflareToken(req.Token)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if token == "" {
		writeErr(w, http.StatusBadRequest, "token cannot be empty")
		return
	}

	var hostname string
	err = s.db.QueryRow(`SELECT hostname FROM domains WHERE id=? AND user_id=?`, id, uid).Scan(&hostname)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}

	// Validate the replacement token and DNS permissions before overwriting a
	// known-good token. Invalid or out-of-scope tokens are never saved.
	zone, record, created, err := s.ensureDomainWithCloudflare(hostname, token, "")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Cloudflare token validation failed: "+err.Error())
		return
	}
	enc, err := s.encryptDomainToken(hostname, token)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not encrypt token")
		return
	}
	tx, txErr := s.db.Begin()
	if txErr == nil {
		_, txErr = tx.Exec(`UPDATE domains SET cloudflare_token_enc=?, cloudflare_zone_id=?, proxied=?,
			status='pending', message='Cloudflare DNS is ready. Click Verify to configure nginx and SSL.'
			WHERE id=? AND user_id=?`, enc, zone.ID, record.Proxied, id, uid)
	}
	if txErr == nil && created {
		_, txErr = tx.Exec(`INSERT INTO managed_dns_records(link_id,domain_id,zone_id,record_id,hostname)
			VALUES(NULL,?,?,?,?) ON CONFLICT(domain_id,hostname) DO UPDATE SET
			link_id=NULL, zone_id=excluded.zone_id, record_id=excluded.record_id`, id, zone.ID, record.ID, hostname)
	}
	if txErr == nil {
		txErr = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if txErr != nil {
		if created {
			cf := &cloudflareClient{apiToken: token, http: defaultHTTPClient()}
			_ = cf.deleteRecordByID(zone.ID, record.ID)
		}
		writeErr(w, http.StatusInternalServerError, "could not save token and DNS ownership")
		return
	}
	s.audit(r, uid, "domain.token_saved", "domain", idString(id), nil)
	d, fetchErr := s.fetchDomain(uid, id)
	if fetchErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not reload domain state")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) handleDomainTokenDelete(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	var status string
	var hasToken bool
	err := s.db.QueryRow(`SELECT status,(cloudflare_token_enc IS NOT NULL AND cloudflare_token_enc!='') FROM domains WHERE id=? AND user_id=?`, id, uid).Scan(&status, &hasToken)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}
	if hasToken && status == "active" {
		writeErr(w, http.StatusConflict, "the active wildcard certificate depends on this token for automatic renewal; delete the domain or replace the token instead")
		return
	}
	var managed int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM managed_dns_records WHERE domain_id=?`, id).Scan(&managed); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not inspect managed DNS records")
		return
	}
	if managed > 0 {
		writeErr(w, http.StatusConflict, "this token manages active Vector DNS records; delete the associated subdomain links/domain first")
		return
	}
	if _, err := s.db.Exec(`UPDATE domains SET cloudflare_token_enc=NULL,cloudflare_zone_id=NULL,
		status='token_missing',message='Cloudflare token removed. Slug links can continue working, but subdomain links are disabled.'
		WHERE id=? AND user_id=?`, id, uid); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove domain token")
		return
	}
	s.audit(r, uid, "domain.token_deleted", "domain", idString(id), nil)
	d, fetchErr := s.fetchDomain(uid, id)
	if fetchErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not reload domain state")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) getDomainTokenForUser(uid int64, hostname string) (string, error) {
	var enc string
	err := s.db.QueryRow(`SELECT COALESCE(cloudflare_token_enc,'') FROM domains WHERE hostname=? AND user_id=?`, hostname, uid).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if enc != "" {
		tok, err := s.decryptDomainToken(hostname, enc)
		if err != nil {
			return "", err
		}
		return tok, nil
	}
	return "", nil
}

func (s *server) getDomainToken(hostname string) (string, error) {
	var enc string
	err := s.db.QueryRow(`SELECT COALESCE(cloudflare_token_enc,'') FROM domains WHERE hostname=?`, hostname).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if enc != "" {
		tok, err := s.decryptDomainToken(hostname, enc)
		if err != nil {
			return "", err
		}
		return tok, nil
	}
	return "", nil
}
