package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBootstrapFailsClosedOnConfigOutage proves the regression fix: a
// database outage while reading setup_complete must never be treated as
// "setup is not complete" (which would keep the bootstrap/setup surface
// open). Every bootstrap entry point must fail closed with 503 instead.
func TestBootstrapFailsClosedOnConfigOutage(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	writeTestBootstrapCredential(t, dataDir, "vector-bootstrap-outage", "correct-bootstrap-password")

	db := openDB(filepath.Join(dataDir, "vector.db"))
	s := &server{db: db, masterKey: []byte("01234567890123456789012345678901")}

	// Simulate a hard database outage by closing the only connection before
	// any config read has populated the runtime cache.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("requireBootstrap", func(t *testing.T) {
		called := false
		protected := s.requireBootstrap(func(w http.ResponseWriter, r *http.Request) {
			called = true
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})
		req := httptest.NewRequest("POST", "http://203.0.113.10:8080/api/setup", strings.NewReader(`{}`))
		req.RemoteAddr = "203.0.113.60:41000"
		w := httptest.NewRecorder()
		protected(w, req)
		if w.Code != http.StatusServiceUnavailable || called {
			t.Fatalf("outage: requireBootstrap status=%d called=%v body=%s (want 503, handler not called)",
				w.Code, called, w.Body.String())
		}
	})

	t.Run("handleBootstrapLogin", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://203.0.113.10:8080/api/setup/bootstrap/login",
			strings.NewReader(`{"username":"vector-bootstrap-outage","password":"correct-bootstrap-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.61:42000"
		w := httptest.NewRecorder()
		s.handleBootstrapLogin(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("outage: handleBootstrapLogin status=%d body=%s (want 503)", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("outage: handleBootstrapLogin issued cookie %q", got)
		}
	})

	t.Run("bootstrapAuthenticated", func(t *testing.T) {
		cred, err := loadBootstrapCredential()
		if err != nil {
			t.Fatal(err)
		}
		token := signBootstrapToken(s.masterKey, bootstrapVerifier(cred), time.Now().Add(time.Minute))
		req := httptest.NewRequest("GET", "http://203.0.113.10:8080/api/setup", nil)
		req.AddCookie(&http.Cookie{Name: bootstrapCookieName, Value: token})
		authenticated, ok := s.bootstrapAuthenticated(req)
		if ok || authenticated {
			t.Fatalf("outage: bootstrapAuthenticated authenticated=%v ok=%v (want false, false)", authenticated, ok)
		}
	})

	t.Run("csrfSource", func(t *testing.T) {
		cred, err := loadBootstrapCredential()
		if err != nil {
			t.Fatal(err)
		}
		token := signBootstrapToken(s.masterKey, bootstrapVerifier(cred), time.Now().Add(time.Minute))
		req := httptest.NewRequest("GET", "http://203.0.113.10:8080/api/csrf", nil)
		req.AddCookie(&http.Cookie{Name: bootstrapCookieName, Value: token})
		if source := s.csrfSource(req); source != "" {
			t.Fatalf("outage: csrfSource trusted bootstrap source %q (want empty)", source)
		}

		w := httptest.NewRecorder()
		s.handleCSRF(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("outage: handleCSRF status=%d body=%s (want 401)", w.Code, w.Body.String())
		}
	})

	t.Run("bootstrapStatus", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://203.0.113.10:8080/api/setup/bootstrap/status", nil)
		req.RemoteAddr = "203.0.113.62:43000"
		required, authenticated, available, message := bootstrapStatus(s, req)
		if available {
			t.Fatalf("outage: bootstrapStatus reported available=true (want false); required=%v authenticated=%v message=%q",
				required, authenticated, message)
		}
		if authenticated {
			t.Fatal("outage: bootstrapStatus reported authenticated=true during a database outage")
		}
	})
}
