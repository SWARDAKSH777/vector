package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestUsesHTTPS(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xfp        string
		tls        bool
		want       bool
	}{
		{name: "direct HTTP setup", remoteAddr: "203.0.113.10:43210", want: false},
		{name: "spoofed forwarded proto from public client", remoteAddr: "203.0.113.10:43210", xfp: "https", want: false},
		{name: "local nginx HTTPS proxy", remoteAddr: "127.0.0.1:43210", xfp: "https", want: true},
		{name: "local nginx HTTP proxy", remoteAddr: "127.0.0.1:43210", xfp: "http", want: false},
		{name: "native TLS", remoteAddr: "203.0.113.10:43210", tls: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://example.test/api/auth/login", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xfp != "" {
				r.Header.Set("X-Forwarded-Proto", tt.xfp)
				if strings.Contains(tt.name, "local nginx") {
					markTrustedProxy(r)
				}
			}
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := requestUsesHTTPS(r); got != tt.want {
				t.Fatalf("requestUsesHTTPS()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoginDuringHTTPSetupIssuesUsableCookie(t *testing.T) {
	db := openDB(t.TempDir() + "/vector.db")
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO users(email,password_hash,role,disabled) VALUES(?,?,'admin',0)`, "admin@example.com", hashPassword("correct-horse-battery-staple")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config(key,value) VALUES('domain','test.dakshu.in')`); err != nil {
		t.Fatal(err)
	}

	s := &server{db: db, masterKey: []byte("01234567890123456789012345678901")}
	r := httptest.NewRequest("POST", "http://129.159.228.8:8080/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"correct-horse-battery-staple"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.20:50000"
	w := httptest.NewRecorder()
	s.handleLogin(w, r)

	if w.Code != 200 {
		t.Fatalf("login status=%d body=%s", w.Code, w.Body.String())
	}
	res := w.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if cookies[0].Secure {
		t.Fatal("HTTP setup login cookie must not be Secure")
	}

	protected := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r2 := httptest.NewRequest("POST", "http://129.159.228.8:8080/api/setup/nginx", nil)
	r2.AddCookie(cookies[0])
	w2 := httptest.NewRecorder()
	protected(w2, r2)
	if w2.Code != 200 {
		t.Fatalf("authenticated follow-up status=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestSetupSubmitCreatesAuthenticatedSession(t *testing.T) {
	db := openDB(t.TempDir() + "/vector.db")
	defer db.Close()

	s := &server{db: db, masterKey: []byte("01234567890123456789012345678901")}
	oldFactory := setupHTTPClientFactory
	setupHTTPClientFactory = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header)}, nil
		})}
	}
	t.Cleanup(func() { setupHTTPClientFactory = oldFactory })
	r := httptest.NewRequest("POST", "http://129.159.228.8:8080/api/setup", strings.NewReader(`{"domain":"links.example.com","admin_email":"admin@example.com","admin_password":"correct-horse-battery-staple","deployment_mode":"single"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.20:50000"
	w := httptest.NewRecorder()
	s.handleSetupSubmit(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("setup status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == setupSessionCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("session cookie %q not found in %#v", setupSessionCookieName, cookies)
	}
	if sessionCookie.Secure {
		t.Fatal("direct HTTP setup cookie must not be Secure")
	}

	protected := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r2 := httptest.NewRequest("POST", "http://129.159.228.8:8080/api/setup/nginx", nil)
	r2.AddCookie(sessionCookie)
	w2 := httptest.NewRecorder()
	protected(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("authenticated follow-up status=%d body=%s", w2.Code, w2.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
