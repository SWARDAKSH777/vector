package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestBootstrapCredential(t *testing.T, dir, username, password string) {
	t.Helper()
	salt := "00112233445566778899aabbccddeeff"
	content := "version=1\n" +
		"username=" + username + "\n" +
		"salt=" + salt + "\n" +
		"password_sha256=" + bootstrapPasswordHash(salt, password) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "bootstrap.conf"), []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapGateProtectsSetup(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	writeTestBootstrapCredential(t, dataDir, "vector-bootstrap-test", "correct-bootstrap-password")

	db := openDB(filepath.Join(dataDir, "vector.db"))
	defer db.Close()
	s := &server{db: db, masterKey: []byte("01234567890123456789012345678901")}

	called := false
	protected := s.requireBootstrap(func(w http.ResponseWriter, r *http.Request) {
		called = true
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	unauthReq := httptest.NewRequest("POST", "http://203.0.113.10:8080/api/setup", strings.NewReader(`{}`))
	unauthReq.RemoteAddr = "203.0.113.50:41000"
	unauthW := httptest.NewRecorder()
	protected(unauthW, unauthReq)
	if unauthW.Code != http.StatusUnauthorized || called {
		t.Fatalf("unauthenticated setup status=%d called=%v body=%s", unauthW.Code, called, unauthW.Body.String())
	}

	loginReq := httptest.NewRequest("POST", "http://203.0.113.10:8080/api/setup/bootstrap/login",
		strings.NewReader(`{"username":"vector-bootstrap-test","password":"correct-bootstrap-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.RemoteAddr = "203.0.113.51:42000"
	loginW := httptest.NewRecorder()
	s.handleBootstrapLogin(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("bootstrap login status=%d body=%s", loginW.Code, loginW.Body.String())
	}
	cookies := loginW.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != bootstrapCookieName {
		t.Fatalf("unexpected bootstrap cookies: %#v", cookies)
	}
	if cookies[0].Secure {
		t.Fatal("direct HTTP bootstrap cookie must not be Secure")
	}

	authReq := httptest.NewRequest("POST", "http://203.0.113.10:8080/api/setup", strings.NewReader(`{}`))
	authReq.RemoteAddr = "203.0.113.51:42001"
	authReq.AddCookie(cookies[0])
	authW := httptest.NewRecorder()
	protected(authW, authReq)
	if authW.Code != http.StatusOK || !called {
		t.Fatalf("authenticated setup status=%d called=%v body=%s", authW.Code, called, authW.Body.String())
	}
}

func TestBootstrapStatusAndCredentialRotation(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	writeTestBootstrapCredential(t, dataDir, "bootstrap-a", "password-a")

	db := openDB(filepath.Join(dataDir, "vector.db"))
	defer db.Close()
	s := &server{db: db, masterKey: []byte("01234567890123456789012345678901")}

	loginReq := httptest.NewRequest("POST", "http://server:8080/api/setup/bootstrap/login",
		strings.NewReader(`{"username":"bootstrap-a","password":"password-a"}`))
	loginReq.RemoteAddr = "198.51.100.81:43000"
	loginW := httptest.NewRecorder()
	s.handleBootstrapLogin(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", loginW.Code, loginW.Body.String())
	}
	cookie := loginW.Result().Cookies()[0]

	statusReq := httptest.NewRequest("GET", "http://server:8080/api/setup/status", nil)
	statusReq.AddCookie(cookie)
	statusW := httptest.NewRecorder()
	s.handleSetupStatus(statusW, statusReq)
	var status map[string]any
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["bootstrap_required"] != true || status["bootstrap_authenticated"] != true || status["bootstrap_available"] != true {
		t.Fatalf("unexpected bootstrap status: %#v", status)
	}

	// A root-side credential reset changes the verifier and must immediately
	// invalidate every bootstrap browser session issued for the old credential.
	writeTestBootstrapCredential(t, dataDir, "bootstrap-b", "password-b")
	rotatedReq := httptest.NewRequest("GET", "http://server:8080/api/setup/status", nil)
	rotatedReq.AddCookie(cookie)
	rotatedW := httptest.NewRecorder()
	s.handleSetupStatus(rotatedW, rotatedReq)
	status = map[string]any{}
	if err := json.Unmarshal(rotatedW.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["bootstrap_authenticated"] != false {
		t.Fatalf("rotated credential did not invalidate old session: %#v", status)
	}
}

func TestBootstrapFailsClosedWithoutCredentialFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	db := openDB(filepath.Join(dataDir, "vector.db"))
	defer db.Close()
	s := &server{db: db, masterKey: []byte("01234567890123456789012345678901")}

	statusReq := httptest.NewRequest("GET", "http://server:8080/api/setup/status", nil)
	statusW := httptest.NewRecorder()
	s.handleSetupStatus(statusW, statusReq)
	var status map[string]any
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["bootstrap_required"] != true || status["bootstrap_available"] != false {
		t.Fatalf("missing verifier must fail closed: %#v", status)
	}

	loginReq := httptest.NewRequest("POST", "http://server:8080/api/setup/bootstrap/login",
		strings.NewReader(`{"username":"anything","password":"anything"}`))
	loginReq.RemoteAddr = "198.51.100.91:44000"
	loginW := httptest.NewRecorder()
	s.handleBootstrapLogin(loginW, loginReq)
	if loginW.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing verifier login status=%d body=%s", loginW.Code, loginW.Body.String())
	}
}

func TestRequestClientIPTrustsForwardingOnlyFromLoopback(t *testing.T) {
	publicReq := httptest.NewRequest("GET", "http://server/", nil)
	publicReq.RemoteAddr = "203.0.113.77:45000"
	publicReq.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := requestClientIP(publicReq); got != "203.0.113.77" {
		t.Fatalf("public spoofed XFF accepted: %q", got)
	}

	proxyReq := httptest.NewRequest("GET", "http://server/", nil)
	proxyReq.RemoteAddr = "127.0.0.1:45001"
	proxyReq.Header.Set("X-Forwarded-For", "198.51.100.22, 127.0.0.1")
	markTrustedProxy(proxyReq)
	if got := requestClientIP(proxyReq); got != "198.51.100.22" {
		t.Fatalf("loopback proxy XFF ignored: %q", got)
	}
}
