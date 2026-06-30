package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func newRegressionServer(t *testing.T) (*server, int64) {
	t.Helper()
	db := openDB(t.TempDir() + "/vector.db")
	t.Cleanup(func() { _ = db.Close() })
	res, err := db.Exec(`INSERT INTO users(email,password_hash,role,disabled) VALUES('admin@example.com','x','admin',0)`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	s := &server{db: db, masterKey: []byte("regression-secret"), publicBaseURL: "https://primary.example.com"}
	s.setConfig("domain", "primary.example.com")
	return s, uid
}

func TestCreateSubdomainRejectsRevokedCloudflareTokenWithoutInsertingLink(t *testing.T) {
	s, uid := newRegressionServer(t)
	enc, err := s.encryptDomainToken("example.com", "revoked-token")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,cloudflare_token_enc,cloudflare_zone_id,is_default)
		VALUES(?,?,'active',?,'zone-revoked',1)`, uid, "example.com", enc)
	if err != nil {
		t.Fatal(err)
	}
	domainID, _ := res.LastInsertId()

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"Invalid API Token"}]}`))
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })

	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{
		"destination_url":"https://example.net",
		"custom_alias":"docs",
		"domain":"example.com",
		"redirect_type":"subdomain"
	}`))
	req = req.WithContext(withUserID(req.Context(), uid))
	rr := httptest.NewRecorder()
	s.handleCreateLink(rr, req)
	if rr.Code == http.StatusCreated {
		t.Fatalf("revoked token unexpectedly created link: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "No link was created") {
		t.Fatalf("expected explicit no-link-created error, got %s", rr.Body.String())
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM links`).Scan(&count)
	if count != 0 {
		t.Fatalf("invalid token inserted %d link rows", count)
	}
	var status string
	_ = s.db.QueryRow(`SELECT status FROM domains WHERE id=?`, domainID).Scan(&status)
	if status != "error" {
		t.Fatalf("domain status=%q, want error", status)
	}
}

func TestCreateSubdomainCreatesDNSBeforeLink(t *testing.T) {
	s, uid := newRegressionServer(t)
	enc, _ := s.encryptDomainToken("example.com", "working-token")
	_, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,cloudflare_token_enc,cloudflare_zone_id,is_default)
		VALUES(?,?,'active',?,'zone-ok',1)`, uid, "example.com", enc)
	if err != nil {
		t.Fatal(err)
	}

	postCount := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"zone-ok","name":"example.com"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-ok/dns_records":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-ok/dns_records":
			postCount++
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"record-1","type":"CNAME","name":"docs.example.com","content":"primary.example.com","proxied":true,"ttl":1}}`))
		default:
			http.Error(w, `{"success":false,"errors":[{"message":"unexpected request"}]}`, http.StatusNotFound)
		}
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })
	invalidateCache("zone-ok")

	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{
		"destination_url":"https://example.net",
		"custom_alias":"docs",
		"domain":"example.com",
		"redirect_type":"subdomain"
	}`))
	req = req.WithContext(withUserID(req.Context(), uid))
	rr := httptest.NewRecorder()
	s.handleCreateLink(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create failed: HTTP %d %s", rr.Code, rr.Body.String())
	}
	if postCount != 1 {
		t.Fatalf("DNS POST count=%d, want 1", postCount)
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE short_code='docs' AND domain='example.com'`).Scan(&count)
	if count != 1 {
		t.Fatalf("link rows=%d, want 1", count)
	}
}

func TestEmptyDomainUsesPersistedDefault(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "other.example.com")
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "default.example.com")

	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{
		"destination_url":"https://example.net",
		"custom_alias":"default-link",
		"redirect_type":"slug"
	}`))
	req = req.WithContext(withUserID(req.Context(), uid))
	rr := httptest.NewRecorder()
	s.handleCreateLink(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create failed: HTTP %d %s", rr.Code, rr.Body.String())
	}
	var response Link
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Domain != "default.example.com" {
		t.Fatalf("domain=%q, want default.example.com", response.Domain)
	}
}

func TestVerifyCustomDomainUsesCloudflareBeforeProvisioning(t *testing.T) {
	s, uid := newRegressionServer(t)
	enc, _ := s.encryptDomainToken("unreachable.invalid", "working-token")
	res, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,cloudflare_token_enc,is_default)
		VALUES(?,?,'pending',?,0)`, uid, "unreachable.invalid", enc)
	if err != nil {
		t.Fatal(err)
	}
	domainID, _ := res.LastInsertId()

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"zone-verify","name":"unreachable.invalid"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-verify/dns_records":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-verify/dns_records":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"record-v","type":"CNAME","name":"unreachable.invalid","content":"primary.example.com","proxied":true,"ttl":1}}`))
		default:
			http.Error(w, `{"success":false,"errors":[{"message":"unexpected request"}]}`, http.StatusNotFound)
		}
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	oldProvision := provisionDomainForVerification
	provisionCalled := false
	provisionDomainForVerification = func(_ *server, domain, port string) (string, error) {
		provisionCalled = true
		return "ok", nil
	}
	t.Cleanup(func() {
		cloudflareAPIBaseURL = oldBase
		provisionDomainForVerification = oldProvision
	})
	invalidateCache("zone-verify")

	req := httptest.NewRequest(http.MethodPost, "/api/domains/1/verify", nil)
	req.SetPathValue("id", strconv.FormatInt(domainID, 10))
	req = req.WithContext(withUserID(req.Context(), uid))
	rr := httptest.NewRecorder()
	s.handleVerifyDomain(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("verify failed: HTTP %d %s", rr.Code, rr.Body.String())
	}
	if !provisionCalled {
		t.Fatal("nginx/SSL provisioning was not called")
	}
	var status string
	_ = s.db.QueryRow(`SELECT status FROM domains WHERE id=?`, domainID).Scan(&status)
	if status != "active" {
		t.Fatalf("domain status=%q, want active", status)
	}
}

func TestSetDefaultDomainUpdatesLinkCreationDefault(t *testing.T) {
	s, uid := newRegressionServer(t)
	first, _ := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "first.example.com")
	_, _ = first.LastInsertId()
	second, _ := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "second.example.com")
	secondID, _ := second.LastInsertId()

	req := httptest.NewRequest(http.MethodPost, "/api/domains/2/default", nil)
	req.SetPathValue("id", strconv.FormatInt(secondID, 10))
	req = req.WithContext(withUserID(req.Context(), uid))
	rr := httptest.NewRecorder()
	s.handleSetDefaultDomain(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set default failed: HTTP %d %s", rr.Code, rr.Body.String())
	}
	if got := s.defaultDomainForUser(uid); got != "second.example.com" {
		t.Fatalf("default domain=%q, want second.example.com", got)
	}
	var defaults int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM domains WHERE user_id=? AND is_default=1`, uid).Scan(&defaults)
	if defaults != 1 {
		t.Fatalf("default row count=%d, want 1", defaults)
	}
}
