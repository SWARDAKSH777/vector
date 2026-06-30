package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSameSlugOnDifferentDomainsResolvesIndependently(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "xx.example.com")
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "yy.example.com")
	if _, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status) VALUES(?,?,?,?,?,'active')`, uid, "test", "https://destination-one.example", "xx.example.com", "slug"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status) VALUES(?,?,?,?,?,'active')`, uid, "test", "https://destination-two.example", "yy.example.com", "slug"); err != nil {
		t.Fatal(err)
	}
	s.setConfig("setup_complete", "true")

	web := fstest.MapFS{"index.html": {Data: []byte("index")}}
	h := s.handleAll(web, http.FileServer(http.FS(web)))
	for _, tc := range []struct {
		host string
		want string
	}{
		{"xx.example.com", "https://destination-one.example"},
		{"yy.example.com", "https://destination-two.example"},
	} {
		r := httptest.NewRequest(http.MethodGet, "https://"+tc.host+"/test", nil)
		r.Host = tc.host
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusFound || w.Header().Get("Location") != tc.want {
			t.Fatalf("host=%s status=%d location=%q want=%q", tc.host, w.Code, w.Header().Get("Location"), tc.want)
		}
	}
}

func TestSlugMatchingIsCaseSensitiveWithinDomain(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "case.example.com")
	for _, item := range []struct{ code, destination string }{
		{"Test", "https://upper.example"},
		{"test", "https://lower.example"},
	} {
		if _, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status) VALUES(?,?,?,?,?,'active')`, uid, item.code, item.destination, "case.example.com", "slug"); err != nil {
			t.Fatal(err)
		}
	}
	s.setConfig("setup_complete", "true")
	web := fstest.MapFS{"index.html": {Data: []byte("index")}}
	h := s.handleAll(web, http.FileServer(http.FS(web)))
	for _, item := range []struct{ path, destination string }{
		{"/Test", "https://upper.example"},
		{"/test", "https://lower.example"},
	} {
		r := httptest.NewRequest(http.MethodGet, "https://case.example.com"+item.path, nil)
		r.Host = "CASE.EXAMPLE.COM"
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusFound || w.Header().Get("Location") != item.destination {
			t.Fatalf("path=%s status=%d location=%q", item.path, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestBlankAliasGeneratesSevenCharacterAlphanumericSlugOnDefaultDomain(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "random.example.com")
	req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(`{
		"destination_url":"https://destination.example/path",
		"redirect_type":"slug"
	}`))
	req = req.WithContext(withUserID(req.Context(), uid))
	w := httptest.NewRecorder()
	s.handleCreateLink(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var link Link
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link.Domain != "random.example.com" {
		t.Fatalf("domain=%q", link.Domain)
	}
	if len(link.ShortCode) != generatedShortCodeLength {
		t.Fatalf("generated code length=%d code=%q", len(link.ShortCode), link.ShortCode)
	}
	for _, ch := range link.ShortCode {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
			t.Fatalf("generated code is not alphanumeric: %q", link.ShortCode)
		}
	}
}

func TestAliasAvailabilityIsScopedByDomainAndExactCase(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "a.example.com")
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "b.example.com")
	_, _ = s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status) VALUES(?,?,?,?,?,'active')`, uid, "Test", "https://destination.example", "a.example.com", "slug")

	check := func(alias, domain string) AliasCheckResult {
		t.Helper()
		q := url.Values{"alias": {alias}, "domain": {domain}, "redirect_type": {"slug"}, "sid": {"test-session"}}
		r := httptest.NewRequest(http.MethodGet, "/api/links/check-alias?"+q.Encode(), nil)
		r = r.WithContext(withUserID(r.Context(), uid))
		w := httptest.NewRecorder()
		s.handleCheckAlias(w, r)
		var result AliasCheckResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
		return result
	}

	if got := check("Test", "a.example.com"); got.Status != "taken" {
		t.Fatalf("exact duplicate status=%q", got.Status)
	}
	if got := check("test", "a.example.com"); got.Status != "available" {
		t.Fatalf("case-distinct status=%q message=%q", got.Status, got.Message)
	}
	if got := check("Test", "b.example.com"); got.Status != "available" {
		t.Fatalf("different-domain status=%q message=%q", got.Status, got.Message)
	}
}

// AliasCheckResult mirrors the JSON response without importing frontend types.
type AliasCheckResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func TestDefaultDomainCannotBeDeletedEvenWhenItIsNotPrimaryOrigin(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "primary.example.com")
	res, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "default.example.net")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	r := httptest.NewRequest(http.MethodDelete, "/api/domains/"+idString(id), nil)
	r.SetPathValue("id", idString(id))
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleDeleteDomain(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "default domain") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeneratedSubdomainPrefixIsSevenCharacterLowercaseAlphanumeric(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "sub.example.com")
	code, err := generateUniqueCode(s.db, "SUB.EXAMPLE.COM", "subdomain")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != generatedShortCodeLength {
		t.Fatalf("generated code length=%d code=%q", len(code), code)
	}
	for _, ch := range code {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			t.Fatalf("generated subdomain prefix is not lowercase alphanumeric: %q", code)
		}
	}
}
