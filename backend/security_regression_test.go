package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestSecurityHeadersDoNotForceHTTPSDuringHTTPBootstrap(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://203.0.113.10:8080/setup", nil)
	r.RemoteAddr = "203.0.113.10:4567"
	w := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(w, r)
	csp := w.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "upgrade-insecure-requests") {
		t.Fatalf("HTTP bootstrap CSP unexpectedly forces HTTPS: %s", csp)
	}
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must not be emitted over the direct HTTP bootstrap listener")
	}
}

func TestSecurityHeadersEnableHTTPSProtectionsBehindLocalNginx(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	r.RemoteAddr = "127.0.0.1:4567"
	r.Header.Set("X-Forwarded-Proto", "https")
	markTrustedProxy(r)
	w := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(w, r)
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "upgrade-insecure-requests") {
		t.Fatal("HTTPS requests should receive upgrade-insecure-requests")
	}
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HTTPS requests should receive HSTS")
	}
}

func TestCSRFProtectionRejectsMissingTokenAndAcceptsDerivedToken(t *testing.T) {
	s, uid := newRegressionServer(t)
	loginReq := httptest.NewRequest(http.MethodPost, "http://example.test/api/auth/login", nil)
	loginW := httptest.NewRecorder()
	if err := s.createSession(loginW, loginReq, uid, true); err != nil {
		t.Fatal(err)
	}
	cookies := loginW.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies=%d, want 1", len(cookies))
	}
	cookie := cookies[0]
	next := s.csrfProtect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	r := httptest.NewRequest(http.MethodPost, "http://example.test/api/links", strings.NewReader(`{}`))
	r.Host = "example.test"
	r.Header.Set("Origin", "http://example.test")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	next.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF token status=%d, want 403", w.Code)
	}

	r2 := httptest.NewRequest(http.MethodPost, "http://example.test/api/links", strings.NewReader(`{}`))
	r2.Host = "example.test"
	r2.Header.Set("Origin", "http://example.test")
	r2.AddCookie(cookie)
	source := cookie.Name + ":" + tokenDigest(cookie.Value)
	r2.Header.Set("X-CSRF-Token", csrfMAC(s.masterKey, source))
	w2 := httptest.NewRecorder()
	next.ServeHTTP(w2, r2)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF token status=%d, want 204: %s", w2.Code, w2.Body.String())
	}
}

func TestCSRFUsesValidBootstrapWhenStaleSetupCookieExists(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	writeTestBootstrapCredential(t, dataDir, "vector-bootstrap-test", "correct-bootstrap-password")

	db := openDB(dataDir + "/vector.db")
	defer db.Close()
	s := &server{db: db, masterKey: []byte("01234567890123456789012345678901")}

	loginReq := httptest.NewRequest(http.MethodPost, "http://example.test:8080/api/setup/bootstrap/login",
		strings.NewReader(`{"username":"vector-bootstrap-test","password":"correct-bootstrap-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	s.handleBootstrapLogin(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("bootstrap login status=%d body=%s", loginW.Code, loginW.Body.String())
	}
	bootstrapCookie := loginW.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "http://example.test:8080/api/security/csrf", nil)
	r.AddCookie(&http.Cookie{Name: setupSessionCookieName, Value: "stale-session-token", Path: "/"})
	r.AddCookie(bootstrapCookie)
	w := httptest.NewRecorder()
	s.handleCSRF(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("valid bootstrap was shadowed by stale setup cookie: status=%d body=%s", w.Code, w.Body.String())
	}
	want := csrfMAC(s.masterKey, bootstrapCookieName+":"+tokenDigest(bootstrapCookie.Value))
	if !strings.Contains(w.Body.String(), want) {
		t.Fatalf("CSRF token was not derived from the valid bootstrap session: %s", w.Body.String())
	}
}

func TestAuthenticatedUserSkipsStaleCookieAndUsesValidSession(t *testing.T) {
	s, uid := newRegressionServer(t)
	loginReq := httptest.NewRequest(http.MethodPost, "http://example.test/api/auth/login", nil)
	loginW := httptest.NewRecorder()
	if err := s.createSession(loginW, loginReq, uid, true); err != nil {
		t.Fatal(err)
	}
	validCookie := loginW.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "http://example.test/api/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: secureSessionCookieName, Value: "stale-secure-token", Path: "/", Secure: true})
	r.AddCookie(validCookie)
	got, err := s.authenticatedUser(r)
	if err != nil || got != uid {
		t.Fatalf("valid session was shadowed by stale cookie: uid=%d err=%v", got, err)
	}
}

func TestUpdateLinkRejectsUnsafeDestinationWithoutChangingDatabase(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status) VALUES(?,?,?,?,?,'active')`, uid, "safe", "https://example.org", "primary.example.com", "slug")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	r := httptest.NewRequest(http.MethodPut, "/api/links/1", strings.NewReader(`{"destination_url":"javascript:alert(1)"}`))
	r.SetPathValue("id", idString(id))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleUpdateLink(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unsafe destination status=%d body=%s", w.Code, w.Body.String())
	}
	var got string
	_ = s.db.QueryRow(`SELECT destination_url FROM links WHERE id=?`, id).Scan(&got)
	if got != "https://example.org" {
		t.Fatalf("destination changed to %q", got)
	}
}

func TestUnlockTokenDoesNotExposeStoredPasswordHash(t *testing.T) {
	s := &server{masterKey: []byte("01234567890123456789012345678901")}
	hash := hashPassword("a-strong-link-password")
	token := s.makeUnlockToken(42, hash, time.Now().Add(time.Hour))
	if strings.Contains(token, hash) || strings.Contains(token, "pbkdf2") {
		t.Fatal("unlock token exposes password verifier")
	}
	if !s.verifyUnlockToken(token, 42, hash) {
		t.Fatal("valid opaque unlock token did not verify")
	}
}

func TestSlugRedirectIsBoundToSelectedDomain(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "a.example.com")
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "b.example.com")
	_, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status) VALUES(?,?,?,?,?,'active')`, uid, "docs", "https://destination.example", "a.example.com", "slug")
	if err != nil {
		t.Fatal(err)
	}
	s.setConfig("setup_complete", "true")

	web := fstest.MapFS{"index.html": {Data: []byte("index")}}
	h := s.handleAll(web, http.FileServer(http.FS(web)))
	wrong := httptest.NewRequest(http.MethodGet, "https://b.example.com/docs", nil)
	wrong.Host = "b.example.com"
	ww := httptest.NewRecorder()
	h(ww, wrong)
	if ww.Code != http.StatusNotFound {
		t.Fatalf("cross-domain redirect status=%d", ww.Code)
	}

	right := httptest.NewRequest(http.MethodGet, "https://a.example.com/docs", nil)
	right.Host = "a.example.com"
	rw := httptest.NewRecorder()
	h(rw, right)
	if rw.Code != http.StatusFound || rw.Header().Get("Location") != "https://destination.example" {
		t.Fatalf("correct-domain redirect status=%d location=%q", rw.Code, rw.Header().Get("Location"))
	}
}

func TestWildcardLinkHostCannotExposeAdminAPI(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "example.com")
	s.setConfig("setup_complete", "true")
	called := false
	h := s.restrictLinkSubdomainSurface(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) }))
	r := httptest.NewRequest(http.MethodPost, "https://docs.example.com/api/auth/login", nil)
	r.Host = "docs.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called || w.Code != http.StatusNotFound {
		t.Fatalf("wildcard admin surface called=%v status=%d", called, w.Code)
	}
}

func TestNonDefaultDomainExposesRedirectsButNotAdminSurface(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "admin.example.com")
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "links.example.net")
	s.setConfig("setup_complete", "true")

	called := false
	h := s.restrictLinkSubdomainSurface(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	redirectReq := httptest.NewRequest(http.MethodGet, "https://links.example.net/docs", nil)
	redirectReq.Host = "links.example.net"
	redirectW := httptest.NewRecorder()
	h.ServeHTTP(redirectW, redirectReq)
	if !called || redirectW.Code != http.StatusNoContent {
		t.Fatalf("public slug path was blocked: called=%v status=%d", called, redirectW.Code)
	}

	called = false
	adminReq := httptest.NewRequest(http.MethodGet, "https://links.example.net/api/auth/me", nil)
	adminReq.Host = "links.example.net"
	adminW := httptest.NewRecorder()
	h.ServeHTTP(adminW, adminReq)
	if called || adminW.Code != http.StatusNotFound {
		t.Fatalf("non-default domain exposed admin API: called=%v status=%d", called, adminW.Code)
	}

	called = false
	defaultReq := httptest.NewRequest(http.MethodGet, "https://admin.example.com/api/auth/me", nil)
	defaultReq.Host = "admin.example.com"
	defaultW := httptest.NewRecorder()
	h.ServeHTTP(defaultW, defaultReq)
	if !called || defaultW.Code != http.StatusNoContent {
		t.Fatalf("default domain admin API was blocked: called=%v status=%d", called, defaultW.Code)
	}
}

func TestProductionHandlerMountsPublicDomainRestriction(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "example.com")
	s.setConfig("setup_complete", "true")

	called := false
	h := s.productionHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "https://evil.example.com/api/auth/me", nil)
	r.Host = "evil.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called || w.Code != http.StatusNotFound {
		t.Fatalf("production chain omitted public-host restriction: called=%v status=%d", called, w.Code)
	}
}

func TestSlugRedirectRejectsExtraPathSegments(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "example.com")
	_, _ = s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status) VALUES(?,?,?,?,?,'active')`, uid, "docs", "https://destination.example", "example.com", "slug")
	s.setConfig("setup_complete", "true")

	web := fstest.MapFS{"index.html": {Data: []byte("index")}}
	h := s.handleAll(web, http.FileServer(http.FS(web)))
	r := httptest.NewRequest(http.MethodGet, "https://example.com/docs/unexpected", nil)
	r.Host = "example.com"
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("extra path segment redirected unexpectedly: status=%d location=%q", w.Code, w.Header().Get("Location"))
	}
}

func TestAppendUTMParametersPreservesExistingQueryAndFragment(t *testing.T) {
	got := appendUTMParameters("https://example.org/path?existing=1#section", "newsletter", "email", "launch 2026")
	want := "https://example.org/path?existing=1&utm_campaign=launch+2026&utm_medium=email&utm_source=newsletter#section"
	if got != want {
		t.Fatalf("UTM destination=%q, want %q", got, want)
	}
}

func TestCSRFTokenEndpointRejectsInventedSessionCookie(t *testing.T) {
	s, _ := newRegressionServer(t)
	r := httptest.NewRequest(http.MethodGet, "http://example.test/api/security/csrf", nil)
	r.AddCookie(&http.Cookie{Name: setupSessionCookieName, Value: "invented-token"})
	w := httptest.NewRecorder()
	s.handleCSRF(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invented session cookie status=%d, want 401", w.Code)
	}
}

func TestDirectIPRedirectUsesConfiguredPrimaryDomain(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "primary.example.com")
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "admin.example.net")
	s.setConfig("domain", "primary.example.com")
	s.setConfig("setup_complete", "true")

	h := s.blockDirectIPAfterSetup(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodGet, "http://203.0.113.20/dashboard", nil)
	r.Host = "203.0.113.20"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "https://primary.example.com/dashboard" {
		t.Fatalf("status=%d location=%q", w.Code, w.Header().Get("Location"))
	}
}

func TestChangingDefaultLinkDomainDoesNotMoveAdminOrigin(t *testing.T) {
	s, uid := newRegressionServer(t)
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',0)`, uid, "primary.example.com")
	_, _ = s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "links.example.net")
	s.setConfig("domain", "primary.example.com")
	s.setConfig("setup_complete", "true")

	called := false
	h := s.restrictLinkSubdomainSurface(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	primaryReq := httptest.NewRequest(http.MethodGet, "https://primary.example.com/api/auth/me", nil)
	primaryReq.Host = "primary.example.com"
	primaryW := httptest.NewRecorder()
	h.ServeHTTP(primaryW, primaryReq)
	if !called || primaryW.Code != http.StatusNoContent {
		t.Fatalf("primary admin origin blocked: called=%v status=%d", called, primaryW.Code)
	}

	called = false
	defaultReq := httptest.NewRequest(http.MethodGet, "https://links.example.net/api/auth/me", nil)
	defaultReq.Host = "links.example.net"
	defaultW := httptest.NewRecorder()
	h.ServeHTTP(defaultW, defaultReq)
	if called || defaultW.Code != http.StatusNotFound {
		t.Fatalf("default link domain exposed admin API: called=%v status=%d", called, defaultW.Code)
	}
}

func TestDeletePrimaryOriginDomainIsBlockedBeforeSideEffects(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?,?,'active',1)`, uid, "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	r := httptest.NewRequest(http.MethodDelete, "/api/domains/1", nil)
	r.SetPathValue("id", idString(id))
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleDeleteDomain(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "primary origin") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM domains WHERE id=?`, id).Scan(&count)
	if count != 1 {
		t.Fatal("primary origin domain was deleted")
	}
}

func TestHealthEndpointAvailableBeforeSetup(t *testing.T) {
	s, _ := newRegressionServer(t)
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/healthz", nil)
	r.Host = "127.0.0.1"
	w := httptest.NewRecorder()
	s.productionHandler(http.HandlerFunc(s.handleHealth)).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSetupDomainCheckDoesNotRequireCSRFRoundTrip(t *testing.T) {
	s := &server{masterKey: []byte("01234567890123456789012345678901")}
	called := false
	h := s.csrfProtect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodPost, "http://example.test/api/setup/check-domain", strings.NewReader(`{}`))
	r.Host = "example.test"
	r.Header.Set("Origin", "http://example.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called || w.Code != http.StatusNoContent {
		t.Fatalf("read-only setup check was blocked by CSRF middleware: called=%v status=%d", called, w.Code)
	}
}

func TestSetupPublicIPFallsBackToInstallerConfiguredIP(t *testing.T) {
	t.Setenv("VECTOR_PUBLIC_IP", "8.8.8.8")
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/setup", nil)
	r.RemoteAddr = "127.0.0.1:45678"
	if got := setupPublicIP(r); got != "8.8.8.8" {
		t.Fatalf("setupPublicIP()=%q, want installer configured public IP", got)
	}
}

func TestSetupPublicIPRejectsPrivateInstallerValue(t *testing.T) {
	t.Setenv("VECTOR_PUBLIC_IP", "10.0.0.5")
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/setup", nil)
	r.RemoteAddr = "127.0.0.1:45678"
	if got := setupPublicIP(r); got != "" {
		t.Fatalf("setupPublicIP()=%q, want empty for private configured IP", got)
	}
}
