package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func closedDatabaseServer(t *testing.T) *server {
	t.Helper()
	db := openDB(t.TempDir() + "/closed.db")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return &server{db: db, masterKey: []byte("closed-database-test-secret")}
}

func TestLoginReportsDatabaseOutageInsteadOfBadCredentials(t *testing.T) {
	s := closedDatabaseServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"outage@example.com","password":"irrelevant-password"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "192.0.2.25:12345"
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want database failure to be 503 rather than 401", w.Code, w.Body.String())
	}
}

func TestRedirectReportsDatabaseOutageInsteadOfMissingLink(t *testing.T) {
	s := closedDatabaseServer(t)
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("index")}}
	var webFS fs.FS = web
	static := http.FileServer(http.FS(webFS))
	r := httptest.NewRequest(http.MethodGet, "https://links.example.com/test-code", nil)
	r.Host = "links.example.com"
	w := httptest.NewRecorder()
	s.handleAll(webFS, static).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want database failure to be 503 rather than 404", w.Code, w.Body.String())
	}
}

func TestVerifyDomainReportsDatabaseOutageInsteadOfMissingDomain(t *testing.T) {
	s := closedDatabaseServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/domains/1/verify", nil)
	r.SetPathValue("id", "1")
	r = r.WithContext(withUserID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handleVerifyDomain(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want database failure to be 500 rather than 404", w.Code, w.Body.String())
	}
}

func TestAuthMiddlewareReportsSessionStoreOutage(t *testing.T) {
	s := closedDatabaseServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: setupSessionCookieName, Value: "test-session-token"})
	w := httptest.NewRecorder()
	s.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want session database failure to be 503 rather than 401", w.Code, w.Body.String())
	}
}

func TestConfigReadFailureDoesNotCacheEmptyValue(t *testing.T) {
	s := closedDatabaseServer(t)
	if value := s.getConfig("setup_complete"); value != "" {
		t.Fatalf("value=%q, want empty fallback", value)
	}
	if _, ok := s.getCachedConfig("setup_complete"); ok {
		t.Fatal("transient database failure poisoned the config cache")
	}
}

func TestSecurityRoutingFailsClosedWhenConfigStoreIsUnavailable(t *testing.T) {
	s := closedDatabaseServer(t)
	nextCalled := false
	h := s.restrictLinkSubdomainSurface(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "https://attacker.example/", nil)
	r.Host = "attacker.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if nextCalled {
		t.Fatal("request reached application while security configuration was unavailable")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want fail-closed 503", w.Code, w.Body.String())
	}
}

func TestAliasCheckReportsDatabaseOutage(t *testing.T) {
	s := closedDatabaseServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/aliases/check?alias=test", nil)
	r = r.WithContext(withUserID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handleCheckAlias(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", w.Code, w.Body.String())
	}
}

func TestGetLinkAndQRCodeReportDatabaseOutage(t *testing.T) {
	s := closedDatabaseServer(t)
	for name, handler := range map[string]http.HandlerFunc{
		"get": s.handleGetLink,
		"qr":  s.handleLinkQRCode,
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/links/1", nil)
			r.SetPathValue("id", "1")
			r = r.WithContext(withUserID(r.Context(), 1))
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s, want 500", w.Code, w.Body.String())
			}
		})
	}
}

func TestGeoMigrationReportsVersionReadOutage(t *testing.T) {
	s := closedDatabaseServer(t)
	if err := s.migrateGeoCountrySource(); err == nil {
		t.Fatal("migration ignored database outage while reading its version")
	}
}
