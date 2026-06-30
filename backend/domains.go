package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"urlshortener/sqlite3local"
)

type Domain struct {
	ID        int64     `json:"id"`
	Hostname  string    `json:"hostname"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	HasToken  bool      `json:"has_token"`
	DNSReady  bool      `json:"dns_ready"`
	Proxied   bool      `json:"proxied"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
}

// Kept as a variable so verification behavior can be regression-tested without
// invoking nginx or certbot.
var provisionDomainForVerification = provisionDomain
var setupHTTPClientFactory = newPublicOnlyHTTPClient

// normalizeHostname validates and canonicalizes an ASCII DNS hostname.
func normalizeHostname(raw string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return "", fmt.Errorf("enter a valid domain, e.g. links.yourdomain.com")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("enter a valid domain, e.g. links.yourdomain.com")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("enter a valid domain, e.g. links.yourdomain.com")
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return "", fmt.Errorf("enter a valid domain, e.g. links.yourdomain.com")
			}
		}
	}
	return host, nil
}

// checkDomainReachable is the setup-stage fallback when no Cloudflare token is supplied.
func checkDomainReachable(domain string) error {
	if err := checkHTTPReachableURL("http://" + domain + "/"); err != nil {
		return fmt.Errorf("cannot reach %q over HTTP: %v\n\nMake sure:\n• The domain has an A/AAAA/CNAME record\n• Port 80 is open in your firewall/security group\n• If Cloudflare proxy is enabled, the record is active", domain, err)
	}
	return nil
}

func newPublicOnlyHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Setup reachability checks are an SSRF-sensitive path. An environment
		// proxy would perform resolution and connection outside this guarded
		// DialContext and could therefore reach private destinations.
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   1,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 6 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid outbound address: %w", err)
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !isPublicDestinationIP(ip) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			return nil, fmt.Errorf("hostname resolves only to private, local, or reserved addresses")
		},
	}
	return &http.Client{
		Timeout:   8 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func isPublicDestinationIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	// Shared address space and benchmarking ranges are not public origins and
	// can route internally in cloud or carrier environments.
	for _, block := range nonPublicDestinationNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicDestinationNetworks = func() []*net.IPNet {
	// Go's IsGlobalUnicast intentionally includes several non-routable ranges.
	// Explicitly reject special-use, documentation, benchmarking and future-use
	// blocks as they can be routed internally by hosting providers.
	cidrs := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4",
		"fc00::/7", "2001:db8::/32", "2001:10::/28", "ff00::/8",
	}
	blocks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		blocks = append(blocks, block)
	}
	return blocks
}()

func checkHTTPReachableURL(url string) error {
	client := setupHTTPClientFactory()
	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Second)
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Vector-Setup-Check/1.0")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil // Any HTTP status proves DNS + port 80 reachability.
	}
	return lastErr
}

// ---- Handlers ----

func (s *server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	rows, err := s.db.Query(`
		SELECT id, hostname, status, message,
		       (cloudflare_token_enc IS NOT NULL AND cloudflare_token_enc != '') AS has_token,
		       (status='active' AND cloudflare_token_enc IS NOT NULL AND cloudflare_token_enc != ''
		        AND cloudflare_zone_id IS NOT NULL AND cloudflare_zone_id != '') AS dns_ready,
		       proxied, is_default, created_at
		FROM domains WHERE user_id=? ORDER BY is_default DESC, created_at ASC`, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list domains")
		return
	}
	defer rows.Close()
	domains := []Domain{}
	for rows.Next() {
		var d Domain
		var msg sql.NullString
		if err := rows.Scan(&d.ID, &d.Hostname, &d.Status, &msg, &d.HasToken, &d.DNSReady, &d.Proxied, &d.IsDefault, &d.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read domain data")
			return
		}
		d.Message = msg.String
		domains = append(domains, d)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list domains")
		return
	}
	writeJSON(w, http.StatusOK, domains)
}

func (s *server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	var req struct {
		Hostname string `json:"hostname"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hostname, err := normalizeHostname(req.Hostname)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	var inheritedEnc, defaultHost string
	inheritErr := s.db.QueryRow(`SELECT hostname, COALESCE(cloudflare_token_enc,'') FROM domains WHERE user_id=? AND is_default=1`, uid).Scan(&defaultHost, &inheritedEnc)
	if inheritErr != nil && !errors.Is(inheritErr, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, "could not inspect the default domain")
		return
	}
	newEnc := ""
	message := "Add a Cloudflare token, then verify the domain."
	if inheritedEnc != "" {
		token, decErr := s.decryptDomainToken(defaultHost, inheritedEnc)
		if decErr != nil {
			writeErr(w, http.StatusInternalServerError, "the default domain token cannot be decrypted; restore the matching master key or replace the token")
			return
		}
		if token != "" {
			newEnc, err = s.encryptDomainToken(hostname, token)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "could not securely inherit the Cloudflare token")
				return
			}
			message = "Cloudflare token inherited from the default domain. Click Verify to configure DNS, nginx and SSL."
		}
	}
	res, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,message,cloudflare_token_enc)
		VALUES(?,?,'pending',?,NULLIF(?,''))`, uid, hostname, message, newEnc)
	if err != nil {
		if sqlite3local.IsConstraint(err) {
			writeErr(w, http.StatusConflict, "that domain has already been added")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not add domain")
		}
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "domain was added but its identifier could not be loaded")
		return
	}
	s.audit(r, uid, "domain.added", "domain", idString(id), map[string]any{"hostname": hostname})
	d, err := s.fetchDomain(uid, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "domain was added but could not be reloaded")
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) handleVerifyDomain(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	d, err := s.fetchDomain(uid, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}

	cfToken, tokenErr := s.getDomainTokenForUser(uid, d.Hostname)
	if tokenErr != nil {
		writeErr(w, http.StatusInternalServerError, "stored Cloudflare token cannot be decrypted; restore the matching master key or replace the token")
		return
	}
	if cfToken != "" {
		zone, record, created, err := s.ensureDomainWithCloudflare(d.Hostname, cfToken, "")
		if err != nil {
			s.setDomainError(uid, id, "Cloudflare verification failed: "+err.Error())
			updated, _ := s.fetchDomain(uid, id)
			writeJSON(w, http.StatusOK, updated)
			return
		}
		tx, txErr := s.db.Begin()
		if txErr == nil {
			_, txErr = tx.Exec(`UPDATE domains SET cloudflare_zone_id=?, proxied=?, status='pending',
				message='Cloudflare DNS is ready. Configuring nginx and SSL…' WHERE id=? AND user_id=?`,
				zone.ID, record.Proxied, id, uid)
		}
		if txErr == nil && created {
			_, txErr = tx.Exec(`INSERT INTO managed_dns_records(link_id,domain_id,zone_id,record_id,hostname)
				VALUES(NULL,?,?,?,?) ON CONFLICT(domain_id,hostname) DO UPDATE SET
				link_id=NULL, zone_id=excluded.zone_id, record_id=excluded.record_id`, id, zone.ID, record.ID, d.Hostname)
		}
		if txErr == nil {
			txErr = tx.Commit()
		} else if tx != nil {
			_ = tx.Rollback()
		}
		if txErr != nil {
			if created {
				cf := &cloudflareClient{apiToken: cfToken, http: defaultHTTPClient()}
				_ = cf.deleteRecordByID(zone.ID, record.ID)
			}
			writeErr(w, http.StatusInternalServerError, "could not save Cloudflare domain state")
			return
		}
	}

	logText, err := provisionDomainForVerification(s, d.Hostname, getenv("INTERNAL_PORT", "8081"))
	if err != nil {
		message := err.Error()
		if strings.TrimSpace(logText) != "" {
			message = strings.TrimSpace(logText) + "\n" + message
		}
		s.setDomainError(uid, id, message)
		updated, fetchErr := s.fetchDomain(uid, id)
		if fetchErr != nil {
			writeErr(w, http.StatusInternalServerError, "could not reload domain state")
			return
		}
		writeJSON(w, http.StatusOK, updated)
		return
	}

	message := "Domain verified; DNS, nginx and SSL are configured."
	if cfToken == "" {
		message = "Domain verified; nginx and SSL are configured via HTTP-01. Add a Cloudflare token to enable subdomain links."
	}
	if _, err := s.db.Exec(`UPDATE domains SET status='active', message=? WHERE id=? AND user_id=?`, message, id, uid); err != nil {
		writeErr(w, http.StatusInternalServerError, "nginx and SSL succeeded, but domain state could not be saved; verify again")
		return
	}
	updated, fetchErr := s.fetchDomain(uid, id)
	if fetchErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not reload domain state")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) handleSetDefaultDomain(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	var status string
	err := s.db.QueryRow(`SELECT status FROM domains WHERE id=? AND user_id=?`, id, uid).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}
	if status != "active" {
		writeErr(w, http.StatusConflict, "only an active, verified domain can be set as default")
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update default domain")
		return
	}
	if _, err = tx.Exec(`UPDATE domains SET is_default=0 WHERE user_id=?`, uid); err == nil {
		_, err = tx.Exec(`UPDATE domains SET is_default=1 WHERE id=? AND user_id=?`, id, uid)
	}
	if err != nil {
		_ = tx.Rollback()
		writeErr(w, http.StatusInternalServerError, "could not update default domain")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update default domain")
		return
	}

	s.audit(r, uid, "domain.default_set", "domain", idString(id), nil)
	d, fetchErr := s.fetchDomain(uid, id)
	if fetchErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not reload default domain")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	s.domainMutationMu.Lock()
	defer s.domainMutationMu.Unlock()
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)
	var hostname, tokenEnc, status string
	var wasDefault bool
	err := s.db.QueryRow(`SELECT hostname,COALESCE(cloudflare_token_enc,''),is_default,status FROM domains WHERE id=? AND user_id=?`, id, uid).Scan(&hostname, &tokenEnc, &wasDefault, &status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}
	primaryOriginValue, configErr := s.getConfigE("domain")
	if configErr != nil {
		writeErr(w, http.StatusInternalServerError, "could not validate the primary origin domain")
		return
	}
	primaryOrigin := strings.ToLower(strings.TrimSpace(primaryOriginValue))
	if primaryOrigin != "" && strings.EqualFold(hostname, primaryOrigin) {
		writeErr(w, http.StatusConflict, "the primary origin domain cannot be deleted; migrate the installation to a new origin or purge Vector instead")
		return
	}
	if wasDefault {
		writeErr(w, http.StatusConflict, "the default domain cannot be deleted; set another active domain as default first")
		return
	}
	if status == "active" {
		var activeCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM domains WHERE user_id=? AND status='active'`, uid).Scan(&activeCount); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not validate active domain state")
			return
		}
		if activeCount <= 1 {
			writeErr(w, http.StatusConflict, "the last active domain cannot be deleted; add and verify another domain first")
			return
		}
	}

	var linkCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE user_id=? AND domain=?`, uid, hostname).Scan(&linkCount); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not inspect domain links")
		return
	}
	if linkCount > 0 {
		writeErr(w, http.StatusConflict, "delete or move all links using this domain first")
		return
	}

	rows, err := s.db.Query(`SELECT zone_id,record_id FROM managed_dns_records WHERE domain_id=?`, id)
	if err != nil {
		writeErr(w, 500, "could not inspect managed DNS records")
		return
	}
	type rec struct{ zone, id string }
	var records []rec
	for rows.Next() {
		var item rec
		if err := rows.Scan(&item.zone, &item.id); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, "could not read managed DNS records")
			return
		}
		records = append(records, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeErr(w, http.StatusInternalServerError, "could not inspect managed DNS records")
		return
	}
	rows.Close()
	if _, err := s.db.Exec(`UPDATE domains SET status='pending', message='Domain deletion is in progress.' WHERE id=? AND user_id=?`, id, uid); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not begin domain deletion")
		return
	}
	if len(records) > 0 {
		token, decErr := s.decryptDomainToken(hostname, tokenEnc)
		if decErr != nil || token == "" {
			s.setDomainError(uid, id, "Deletion paused: restore a valid Cloudflare token so Vector can safely remove its DNS records.")
			writeErr(w, http.StatusConflict, "restore a valid Cloudflare token before deleting this domain so Vector can safely remove its DNS records")
			return
		}
		cf := &cloudflareClient{apiToken: token, http: defaultHTTPClient()}
		for _, item := range records {
			if err := cf.deleteRecordByID(item.zone, item.id); err != nil {
				s.setDomainError(uid, id, "Deletion paused after a Cloudflare DNS cleanup failure: "+err.Error())
				writeErr(w, http.StatusBadGateway, "Cloudflare DNS cleanup failed; domain was not deleted: "+err.Error())
				return
			}
		}
	}
	if resp, err := callHelper(helperRequest{Action: "remove", Domain: hostname}); err != nil || !resp.OK {
		message := "privileged helper failed"
		if err != nil {
			message = err.Error()
		} else if resp.Error != "" {
			message = resp.Error
		}
		s.setDomainError(uid, id, "Deletion paused after nginx/TLS cleanup failed: "+message)
		writeErr(w, http.StatusServiceUnavailable, "could not remove nginx/TLS configuration: "+message)
		return
	}
	res, err := s.db.Exec(`DELETE FROM domains WHERE id=? AND user_id=?`, id, uid)
	if err != nil {
		s.setDomainError(uid, id, "External cleanup completed, but the database row could not be removed. Retry deletion.")
		writeErr(w, http.StatusInternalServerError, "could not delete domain")
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not confirm domain deletion")
		return
	}
	if n == 0 {
		writeErr(w, 404, "domain not found")
		return
	}
	s.audit(r, uid, "domain.deleted", "domain", idString(id), map[string]any{"hostname": hostname})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGetSubdomainBlocklist returns taken hostnames for a domain's CF zone.
func (s *server) handleGetSubdomainBlocklist(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)

	var hostname, zoneID, tokenEnc, status string
	err := s.db.QueryRow(`SELECT hostname, COALESCE(cloudflare_zone_id,''), COALESCE(cloudflare_token_enc,''), status
		FROM domains WHERE id=? AND user_id=?`, id, uid).Scan(&hostname, &zoneID, &tokenEnc, &status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "domain not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load domain")
		return
	}
	if status != "active" {
		writeErr(w, http.StatusConflict, "domain must be active before checking subdomain availability")
		return
	}
	if zoneID == "" || tokenEnc == "" {
		writeErr(w, http.StatusConflict, "a healthy Cloudflare token is required for subdomain links")
		return
	}
	tok, err := s.decryptDomainToken(hostname, tokenEnc)
	if err != nil || tok == "" {
		writeErr(w, http.StatusConflict, "stored Cloudflare token is invalid; replace it in Domains")
		return
	}
	cf := &cloudflareClient{apiToken: tok, http: defaultHTTPClient()}
	blocked, err := cf.GetSubdomainBlocklist(zoneID)
	if err != nil {
		s.setDomainError(uid, id, "Cloudflare API check failed: "+err.Error())
		writeErr(w, http.StatusBadGateway, "Cloudflare DNS check failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, blocked)
}

func (s *server) ensureDomainWithCloudflare(hostname, cfToken, primaryOriginIP string) (*cfZone, *cfDNSRecord, bool, error) {
	targetHost, err := s.getConfigE("domain")
	if err != nil {
		return nil, nil, false, fmt.Errorf("read primary origin: %w", err)
	}
	cf := &cloudflareClient{apiToken: cfToken, targetHost: targetHost, primaryOriginIP: primaryOriginIP, http: defaultHTTPClient()}
	zone, err := cf.findZoneForHostname(hostname)
	if err != nil {
		return nil, nil, false, err
	}
	record, created, err := cf.ensureRecord(zone.ID, hostname)
	if err != nil {
		return nil, nil, false, err
	}
	return zone, record, created, nil
}

// resolveDomainWithCF is used by setup and now correctly marks failures as errors.
func (s *server) resolveDomainWithCF(id int64, hostname, cfToken, primaryOriginIP string) error {
	zone, record, created, err := s.ensureDomainWithCloudflare(hostname, cfToken, primaryOriginIP)
	if err != nil {
		_, updateErr := s.db.Exec(`UPDATE domains SET status='error', message=? WHERE id=?`, "Cloudflare DNS configuration failed: "+err.Error(), id)
		if updateErr != nil {
			return fmt.Errorf("%v; additionally could not save the domain error: %w", err, updateErr)
		}
		return err
	}
	tx, txErr := s.db.Begin()
	if txErr == nil {
		_, txErr = tx.Exec(`UPDATE domains SET cloudflare_zone_id=?, status='pending', message=?, proxied=? WHERE id=?`,
			zone.ID, fmt.Sprintf("Cloudflare DNS confirmed in zone %q.", zone.Name), record.Proxied, id)
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
			cf := &cloudflareClient{apiToken: cfToken, http: defaultHTTPClient()}
			_ = cf.deleteRecordByID(zone.ID, record.ID)
		}
		return txErr
	}
	return nil
}

func (s *server) recordManagedDNS(domainID, linkID int64, zoneID, recordID, hostname string) error {
	var link any
	if linkID > 0 {
		link = linkID
	}
	_, err := s.db.Exec(`INSERT INTO managed_dns_records(link_id,domain_id,zone_id,record_id,hostname)
		VALUES(?,?,?,?,?) ON CONFLICT(domain_id,hostname) DO UPDATE SET
		link_id=excluded.link_id, zone_id=excluded.zone_id, record_id=excluded.record_id`,
		link, domainID, zoneID, recordID, hostname)
	return err
}

func (s *server) setDomainError(uid, id int64, message string) {
	_, _ = s.db.Exec(`UPDATE domains SET status='error', message=? WHERE id=? AND user_id=?`, message, id, uid)
}

func (s *server) fetchDomain(uid, id int64) (Domain, error) {
	var d Domain
	var msg sql.NullString
	err := s.db.QueryRow(`
		SELECT id, hostname, status, message,
		       (cloudflare_token_enc IS NOT NULL AND cloudflare_token_enc != '') AS has_token,
		       (status='active' AND cloudflare_token_enc IS NOT NULL AND cloudflare_token_enc != ''
		        AND cloudflare_zone_id IS NOT NULL AND cloudflare_zone_id != '') AS dns_ready,
		       proxied, is_default, created_at
		FROM domains WHERE id=? AND user_id=?`, id, uid).
		Scan(&d.ID, &d.Hostname, &d.Status, &msg, &d.HasToken, &d.DNSReady, &d.Proxied, &d.IsDefault, &d.CreatedAt)
	d.Message = msg.String
	return d, err
}

func (s *server) defaultDomainForUserE(uid int64) (string, error) {
	var hostname string
	err := s.db.QueryRow(`SELECT hostname FROM domains WHERE user_id=? AND is_default=1 AND status='active'`, uid).Scan(&hostname)
	if err == nil {
		return hostname, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = s.db.QueryRow(`SELECT hostname FROM domains WHERE user_id=? AND status='active' ORDER BY created_at ASC LIMIT 1`, uid).Scan(&hostname)
	if err == nil {
		return hostname, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return s.getConfigE("domain")
}

func (s *server) defaultDomainForUser(uid int64) string {
	hostname, _ := s.defaultDomainForUserE(uid)
	return hostname
}
