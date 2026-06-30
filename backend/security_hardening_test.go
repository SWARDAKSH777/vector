package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoopbackForwardedHeadersRequireProxySecret(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.test/", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := requestClientIP(r); got != "127.0.0.1" {
		t.Fatalf("untrusted local process spoofed client IP: %q", got)
	}
	if requestUsesHTTPS(r) {
		t.Fatal("untrusted local process spoofed HTTPS")
	}
	markTrustedProxy(r)
	if got := requestClientIP(r); got != "8.8.8.8" {
		t.Fatalf("trusted proxy client IP=%q", got)
	}
	if !requestUsesHTTPS(r) {
		t.Fatal("trusted proxy HTTPS was not recognized")
	}
}

func TestDisabledLegacyUserSessionIsRejected(t *testing.T) {
	db := openDB(t.TempDir() + "/vector.db")
	defer db.Close()
	res, err := db.Exec(`INSERT INTO users(email,password_hash,role,disabled) VALUES('disabled@example.com','x','disabled',1)`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	token := "legacy-disabled-session"
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO sessions(token_hash,user_id,created_at,last_seen_at,expires_at,user_agent_hash) VALUES(?,?,?,?,?,?)`, tokenDigest(token), uid, now, now, now.Add(time.Hour), userAgentDigest("test")); err != nil {
		t.Fatal(err)
	}
	s := &server{db: db}
	r := httptest.NewRequest("GET", "http://example.test/api/auth/me", nil)
	r.Header.Set("User-Agent", "test")
	if _, err := s.authenticatedUserToken(r, token); err == nil {
		t.Fatal("disabled non-admin session was accepted")
	}
}

func TestRateLimiterBucketCountIsBounded(t *testing.T) {
	rl := newRateLimiterWithLimit(1, 1, 64)
	for i := 0; i < 1000; i++ {
		rl.allow(idString(int64(i)))
	}
	if len(rl.buckets) > 64 {
		t.Fatalf("rate limiter buckets=%d, want <=64", len(rl.buckets))
	}
}

func TestServerNameConflictDetection(t *testing.T) {
	if !serverNameOverlaps("example.com", "example.com", false) {
		t.Fatal("exact server name conflict not detected")
	}
	if !serverNameOverlaps("*.example.com", "example.com", true) {
		t.Fatal("wildcard server name conflict not detected")
	}
	if serverNameOverlaps("other.example", "example.com", true) {
		t.Fatal("unrelated server name reported as conflict")
	}
}

func TestSameOriginRequestRejectsDifferentPort(t *testing.T) {
	r := httptest.NewRequest("POST", "http://example.test:8080/api/auth/login", nil)
	r.Host = "example.test:8080"
	r.Header.Set("Origin", "http://example.test:9090")
	if sameOriginRequest(r) {
		t.Fatal("different-port origin was accepted as same origin")
	}
}

func TestSameOriginRequestAcceptsEquivalentDefaultPort(t *testing.T) {
	r := httptest.NewRequest("POST", "https://example.test/api/auth/login", nil)
	r.Host = "example.test"
	r.Header.Set("Origin", "https://example.test:443")
	if !sameOriginRequest(r) {
		t.Fatal("equivalent HTTPS default port was rejected")
	}
}
