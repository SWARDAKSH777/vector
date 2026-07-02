package main

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

func TestCreateLinkValidatesEqualFieldValuesIndependently(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "primary.example.com")
	tooLongForTag := strings.Repeat("x", maxTagBytes+1)
	body := `{"destination_url":"https://example.org","tag":` + strconvQuote(tooLongForTag) + `,"notes":` + strconvQuote(tooLongForTag) + `}`
	r := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleCreateLink(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "tag") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestExpiredPasswordUnlockCannotRedirect(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "primary.example.com")
	hash, err := hashPasswordWithError("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status,password_hash,expires_at)
		VALUES(?,?,?,?,?,'active',?,?)`, uid, "locked", "https://destination.example", "primary.example.com", "slug", hash, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	form := url.Values{"password": {"correct horse battery staple"}}
	r := httptest.NewRequest(http.MethodPost, "https://primary.example.com/locked/unlock", strings.NewReader(form.Encode()))
	r.Host = "primary.example.com"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handlePasswordUnlock(w, r)
	if w.Code != http.StatusGone || w.Header().Get("Location") != "" {
		t.Fatalf("expired unlock status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	var status string
	var clicks int64
	if err := s.db.QueryRow(`SELECT status,lifetime_click_count FROM links WHERE id=?`, id).Scan(&status, &clicks); err != nil {
		t.Fatal(err)
	}
	if status != "expired" || clicks != 0 {
		t.Fatalf("status=%q clicks=%d", status, clicks)
	}
}

func TestClickUpdateRechecksExpirationAtomically(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,expires_at)
		VALUES(?,?,?,?, 'active',?)`, uid, "expired", "https://example.org", "primary.example.com", time.Now().UTC().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	r := httptest.NewRequest(http.MethodGet, "https://primary.example.com/expired", nil)
	w := httptest.NewRecorder()
	s.logClickAndRedirect(w, r, id, "https://example.org")
	if w.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var clicks int64
	_ = s.db.QueryRow(`SELECT lifetime_click_count FROM links WHERE id=?`, id).Scan(&clicks)
	if clicks != 0 {
		t.Fatalf("expired link counted %d clicks", clicks)
	}
}

func TestCloudflareDNSCacheIsScopedToToken(t *testing.T) {
	var calls atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"` + token + `","type":"A","name":"example.com","content":"1.1.1.1"}]}`))
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })
	invalidateCache("zone-token-test")

	first := &cloudflareClient{apiToken: "first", http: mock.Client()}
	second := &cloudflareClient{apiToken: "second", http: mock.Client()}
	firstRecords, err := first.listZoneRecords("zone-token-test")
	if err != nil {
		t.Fatal(err)
	}
	secondRecords, err := second.listZoneRecords("zone-token-test")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || firstRecords[0].ID != "first" || secondRecords[0].ID != "second" {
		t.Fatalf("calls=%d first=%q second=%q", calls.Load(), firstRecords[0].ID, secondRecords[0].ID)
	}
}

func TestPublicDestinationIPRejectsDocumentationAndFutureUse(t *testing.T) {
	for _, raw := range []string{"192.0.2.1", "198.51.100.2", "203.0.113.3", "240.0.0.1", "2001:db8::1"} {
		if isPublicDestinationIP(net.ParseIP(raw)) {
			t.Fatalf("special-use address %s was accepted", raw)
		}
	}
}

func TestWriteJSONDoesNotCommitInvalidRawMessage(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]json.RawMessage{"bad": json.RawMessage(`{`)})
	if w.Code != http.StatusInternalServerError || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestWeakArgon2HashIsMarkedForUpgrade(t *testing.T) {
	salt := []byte("0123456789abcdef")
	hash := argon2.IDKey([]byte("password"), salt, 1, 8*1024, 1, 16)
	encoded := "argon2id$v=19$t=1,m=8192,p=1$" + rawStdBase64(salt) + "$" + rawStdBase64(hash)
	if !verifyArgon2id("password", encoded) {
		t.Fatal("valid weak legacy hash did not verify")
	}
	if !passwordHashNeedsUpgrade(encoded) {
		t.Fatal("weak Argon2id hash was not marked for upgrade")
	}
	strong, err := hashArgon2id("password")
	if err != nil {
		t.Fatal(err)
	}
	if passwordHashNeedsUpgrade(strong) {
		t.Fatal("current Argon2id hash was incorrectly marked for upgrade")
	}
}

func rawStdBase64(value []byte) string {
	return base64.RawStdEncoding.EncodeToString(value)
}

func TestPrivilegedHelperRefusesToReplaceRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.sock")
	if err := os.WriteFile(path, []byte("do not remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, _, err := privilegedHelperListener(path)
	if ln != nil {
		_ = ln.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "refusing to replace") {
		t.Fatalf("unexpected error: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "do not remove" {
		t.Fatalf("regular file was modified: data=%q err=%v", data, readErr)
	}
}

func TestSuccessfulPasswordUnlockCountsExactlyOnce(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "primary.example.com")
	hash, err := hashPasswordWithError("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status,password_hash)
		VALUES(?,?,?,?,?,'active',?)`, uid, "counted", "https://destination.example", "primary.example.com", "slug", hash)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	form := url.Values{"password": {"correct horse battery staple"}}
	r := httptest.NewRequest(http.MethodPost, "https://primary.example.com/counted/unlock", strings.NewReader(form.Encode()))
	r.Host = "primary.example.com"
	r.RemoteAddr = "192.0.2.77:43123"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handlePasswordUnlock(w, r)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://destination.example" {
		t.Fatalf("status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	var visible, lifetime int64
	if err := s.db.QueryRow(`SELECT click_count,lifetime_click_count FROM links WHERE id=?`, id).Scan(&visible, &lifetime); err != nil {
		t.Fatal(err)
	}
	if visible != 1 || lifetime != 1 {
		t.Fatalf("visible=%d lifetime=%d", visible, lifetime)
	}
}

func TestDecodeJSONRejectsJSONPrefixContentTypes(t *testing.T) {
	for _, contentType := range []string{"application/jsonp", "application/json-evil", "application/json; charset"} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ok":true}`))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		var body map[string]bool
		if err := decodeJSON(w, r, &body); err == nil {
			t.Fatalf("content type %q was accepted", contentType)
		}
	}
}

func TestDecodeJSONAcceptsStandardAndStructuredJSONTypes(t *testing.T) {
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", "application/problem+json"} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ok":true}`))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		var body map[string]bool
		if err := decodeJSON(w, r, &body); err != nil || !body["ok"] {
			t.Fatalf("content type %q failed: body=%v err=%v", contentType, body, err)
		}
	}
}

func TestSetupRejectsMissingDomainToPreventAdminLockout(t *testing.T) {
	s, _ := newRegressionServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"domain":"","admin_email":"admin@example.com","admin_password":"correct-horse-battery-staple","deployment_mode":"single"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSetupSubmit(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "domain is required") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if s.getConfig("setup_complete") == "true" {
		t.Fatal("setup was incorrectly completed without a usable administrator origin")
	}
}

func TestCreateLinkRejectsConflictingExpiryInputs(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "primary.example.com")
	body := `{"destination_url":"https://example.org","expires_in":"1h","expires_at":"2099-01-01"}`
	r := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleCreateLink(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "only one") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateLinkRejectsConflictingClearAndSetInputs(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status) VALUES(?,?,?,?, 'active')`, uid, "conflict", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	for _, body := range []string{
		`{"clear_password":true,"password":"a sufficiently long password"}`,
		`{"clear_expiry":true,"expires_in":"1h"}`,
		`{"expires_in":"1h","expires_at":"2099-01-01"}`,
		`{"clear_max_clicks":true,"max_clicks":100}`,
	} {
		r := httptest.NewRequest(http.MethodPut, "/api/links/1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("id", idString(id))
		r = r.WithContext(withUserID(r.Context(), uid))
		w := httptest.NewRecorder()
		s.handleUpdateLink(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, w.Code, w.Body.String())
		}
	}
}

func TestExpiredLinkCannotBeReactivatedWithoutClearingExpiry(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,expires_at) VALUES(?,?,?,?, 'expired',?)`, uid, "old", "https://example.org", "primary.example.com", time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	reactivate := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/links/1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("id", idString(id))
		r = r.WithContext(withUserID(r.Context(), uid))
		w := httptest.NewRecorder()
		s.handleUpdateLink(w, r)
		return w
	}
	if w := reactivate(`{"status":"active"}`); w.Code != http.StatusConflict {
		t.Fatalf("reactivation status=%d body=%s", w.Code, w.Body.String())
	}
	if w := reactivate(`{"status":"active","clear_expiry":true}`); w.Code != http.StatusOK {
		t.Fatalf("clear-and-reactivate status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestExhaustedLinkCannotBeReactivatedWithoutClearingLimit(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,max_clicks,lifetime_click_count) VALUES(?,?,?,?, 'paused',5,5)`, uid, "full", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	reactivate := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/links/1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("id", idString(id))
		r = r.WithContext(withUserID(r.Context(), uid))
		w := httptest.NewRecorder()
		s.handleUpdateLink(w, r)
		return w
	}
	if w := reactivate(`{"status":"active"}`); w.Code != http.StatusConflict {
		t.Fatalf("reactivation status=%d body=%s", w.Code, w.Body.String())
	}
	if w := reactivate(`{"status":"active","clear_max_clicks":true}`); w.Code != http.StatusOK {
		t.Fatalf("clear-and-reactivate status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEncryptedTokenMigrationIsTransactionalWithSingleConnection(t *testing.T) {
	s, uid := newRegressionServer(t)
	legacyKey := []byte("legacy-session-secret")
	s.legacySecret = append([]byte(nil), legacyKey...)
	legacy, err := encryptAES(legacyKey, "cloudflare-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO config(key,value) VALUES('session_secret','legacy-value')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,cloudflare_token_enc,is_default) VALUES(?,?,'pending',?,1)`, uid, "migration.example.com", legacy); err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	if err := s.migrateEncryptedSecrets(); err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err := s.db.QueryRow(`SELECT cloudflare_token_enc FROM domains WHERE hostname='migration.example.com'`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "v2:") {
		t.Fatalf("token was not migrated: %q", encoded)
	}
	plain, err := s.decryptDomainToken("migration.example.com", encoded)
	if err != nil || plain != "cloudflare-token" {
		t.Fatalf("migrated token=%q err=%v", plain, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM config WHERE key='session_secret'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(s.legacySecret) != 0 {
		t.Fatalf("legacy key retained: rows=%d memory=%d", count, len(s.legacySecret))
	}
}

func TestFailedEncryptedTokenMigrationRetainsLegacyKey(t *testing.T) {
	s, uid := newRegressionServer(t)
	s.legacySecret = []byte("legacy-session-secret")
	if _, err := s.db.Exec(`INSERT INTO config(key,value) VALUES('session_secret','legacy-value')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,cloudflare_token_enc,is_default) VALUES(?,?,'pending','not-valid-ciphertext',1)`, uid, "broken.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateEncryptedSecrets(); err == nil {
		t.Fatal("malformed legacy token migration unexpectedly succeeded")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM config WHERE key='session_secret'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(s.legacySecret) == 0 {
		t.Fatalf("legacy key was removed after failed migration: rows=%d memory=%d", count, len(s.legacySecret))
	}
}

func TestPasswordUnlockDoesNotAcceptPasswordFromURL(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "primary.example.com")
	hash, err := hashPasswordWithError("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status,password_hash)
		VALUES(?,?,?,?,?,'active',?)`, uid, "query-secret", "https://destination.example", "primary.example.com", "slug", hash)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://primary.example.com/query-secret/unlock?password=correct+horse+battery+staple", strings.NewReader(""))
	r.Host = "primary.example.com"
	r.RemoteAddr = "192.0.2.88:43123"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handlePasswordUnlock(w, r)
	if w.Code != http.StatusUnauthorized || w.Header().Get("Location") != "" {
		t.Fatalf("status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
}

func TestPasswordUnlockRejectsUnexpectedContentType(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "primary.example.com")
	hash, err := hashPasswordWithError("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status,password_hash)
		VALUES(?,?,?,?,?,'active',?)`, uid, "json-secret", "https://destination.example", "primary.example.com", "slug", hash)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://primary.example.com/json-secret/unlock", strings.NewReader(`{"password":"correct horse battery staple"}`))
	r.Host = "primary.example.com"
	r.RemoteAddr = "192.0.2.89:43123"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handlePasswordUnlock(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestManagedSubdomainDeletePausesAfterDNSCleanupWhenLocalDeleteFails(t *testing.T) {
	s, uid := newRegressionServer(t)
	enc, err := s.encryptDomainToken("example.com", "working-token")
	if err != nil {
		t.Fatal(err)
	}
	domainRes, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,cloudflare_token_enc,cloudflare_zone_id,is_default)
		VALUES(?,?,'active',?,'zone-delete',1)`, uid, "example.com", enc)
	if err != nil {
		t.Fatal(err)
	}
	domainID, _ := domainRes.LastInsertId()
	linkRes, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status)
		VALUES(?,?,?,?,?,'active')`, uid, "managed", "https://destination.example", "example.com", "subdomain")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := linkRes.LastInsertId()
	if _, err = s.db.Exec(`INSERT INTO managed_dns_records(link_id,domain_id,zone_id,record_id,hostname)
		VALUES(?,?,?,?,?)`, linkID, domainID, "zone-delete", "record-delete", "managed.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER block_link_delete BEFORE DELETE ON links BEGIN SELECT RAISE(ABORT,'delete blocked'); END`); err != nil {
		t.Fatal(err)
	}

	deleteCalls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, `{"success":false,"errors":[{"message":"unexpected"}]}`, http.StatusNotFound)
			return
		}
		deleteCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{}}`))
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })

	r := httptest.NewRequest(http.MethodDelete, "/api/links/1", nil)
	r.SetPathValue("id", strconv.FormatInt(linkID, 10))
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleDeleteLink(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if deleteCalls != 1 {
		t.Fatalf("Cloudflare delete calls=%d, want 1", deleteCalls)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM links WHERE id=?`, linkID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "paused" {
		t.Fatalf("status=%q, want paused after external cleanup/local failure", status)
	}
}

func TestManagedSubdomainDeleteRestoresStatusWhenCloudflareCleanupFails(t *testing.T) {
	s, uid := newRegressionServer(t)
	enc, err := s.encryptDomainToken("example.com", "working-token")
	if err != nil {
		t.Fatal(err)
	}
	domainRes, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,cloudflare_token_enc,cloudflare_zone_id,is_default)
		VALUES(?,?,'active',?,'zone-fail',1)`, uid, "example.com", enc)
	if err != nil {
		t.Fatal(err)
	}
	domainID, _ := domainRes.LastInsertId()
	linkRes, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status)
		VALUES(?,?,?,?,?,'active')`, uid, "managed-fail", "https://destination.example", "example.com", "subdomain")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := linkRes.LastInsertId()
	if _, err = s.db.Exec(`INSERT INTO managed_dns_records(link_id,domain_id,zone_id,record_id,hostname)
		VALUES(?,?,?,?,?)`, linkID, domainID, "zone-fail", "record-fail", "managed-fail.example.com"); err != nil {
		t.Fatal(err)
	}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"upstream unavailable"}]}`))
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })

	r := httptest.NewRequest(http.MethodDelete, "/api/links/1", nil)
	r.SetPathValue("id", strconv.FormatInt(linkID, 10))
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleDeleteLink(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM links WHERE id=?`, linkID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Fatalf("status=%q, want original active status restored", status)
	}
}
