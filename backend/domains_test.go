package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"Test.Dakshu.IN.", "test.dakshu.in", true},
		{"links.example.com", "links.example.com", true},
		{"example.com/path", "", false},
		{"example.com:443", "", false},
		{"127.0.0.1", "", false},
		{"-bad.example.com", "", false},
		{"bad-.example.com", "", false},
		{"singlelabel", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := normalizeHostname(tc.input)
			if tc.ok && err != nil {
				t.Fatalf("normalizeHostname(%q) error: %v", tc.input, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("normalizeHostname(%q) unexpectedly succeeded with %q", tc.input, got)
			}
			if got != tc.want {
				t.Fatalf("normalizeHostname(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCheckHTTPReachableURLAccepts404(t *testing.T) {
	original := setupHTTPClientFactory
	setupHTTPClientFactory = func() *http.Client {
		return &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	t.Cleanup(func() { setupHTTPClientFactory = original })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if err := checkHTTPReachableURL(srv.URL); err != nil {
		t.Fatalf("404 must count as reachable during setup: %v", err)
	}
}

func TestCheckHTTPReachableURLDoesNotFollowRedirect(t *testing.T) {
	original := setupHTTPClientFactory
	setupHTTPClientFactory = func() *http.Client {
		return &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	t.Cleanup(func() { setupHTTPClientFactory = original })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://127.0.0.1:1/", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	if err := checkHTTPReachableURL(srv.URL); err != nil {
		t.Fatalf("redirect response must count as reachable without following HTTPS: %v", err)
	}
}

func TestNginxTemplatesDoNotContainInjectedHostnameSyntax(t *testing.T) {
	bad := "example.com; return 200"
	if _, err := normalizeHostname(bad); err == nil || !strings.Contains(err.Error(), "valid domain") {
		t.Fatalf("unsafe hostname was not rejected: %q", bad)
	}
}

func TestPublicDestinationIPRejectsInternalAndReservedNetworks(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "::1", "fc00::1"} {
		if isPublicDestinationIP(net.ParseIP(raw)) {
			t.Fatalf("internal/reserved address %s was accepted", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !isPublicDestinationIP(net.ParseIP(raw)) {
			t.Fatalf("public address %s was rejected", raw)
		}
	}
}

func TestCloudflareDeleteRecordTreatsNotFoundAsSuccess(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method=%s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"record not found"}]}`))
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })
	client := &cloudflareClient{apiToken: "token", http: mock.Client()}
	if err := client.deleteRecordByID("zone", "missing"); err != nil {
		t.Fatalf("404 delete should be idempotent: %v", err)
	}
}

func TestCheckSetupDomainAcceptsValidCloudflareZoneBeforeRecordExists(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "/dns_records"):
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"zone-1","name":"example.com"}]}`))
		case strings.Contains(r.URL.Path, "/dns_records"):
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()

	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	invalidateCache("zone-1")
	t.Cleanup(func() {
		cloudflareAPIBaseURL = oldBase
		invalidateCache("zone-1")
	})

	method, err := checkSetupDomain("url.example.com", "valid-token")
	if err != nil {
		t.Fatalf("fresh Cloudflare hostname should be accepted before DNS creation: %v", err)
	}
	if method != "cloudflare_api_pending_dns" {
		t.Fatalf("method=%q, want cloudflare_api_pending_dns", method)
	}
}

func TestInitialSetupRejectsPrimaryDNSPointingElsewhere(t *testing.T) {
	tests := []struct {
		name    string
		record  string
		content string
	}{
		{name: "mismatched A record", record: "A", content: "203.0.113.9"},
		{name: "unverifiable CNAME", record: "CNAME", content: "other.example.net"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet || r.URL.Path != "/zones/zone-primary/dns_records" {
					http.Error(w, `{"success":false}`, http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"record-1","type":"` + tc.record + `","name":"links.example.com","content":"` + tc.content + `","proxied":true,"ttl":1}]}`))
			}))
			defer mock.Close()
			oldBase := cloudflareAPIBaseURL
			cloudflareAPIBaseURL = mock.URL
			t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })

			client := &cloudflareClient{
				apiToken:        "setup-token-" + tc.name,
				targetHost:      "links.example.com",
				primaryOriginIP: "8.8.8.8",
				http:            mock.Client(),
			}
			invalidateCache("zone-primary")
			if _, _, err := client.ensureRecord("zone-primary", "links.example.com"); err == nil {
				t.Fatal("setup accepted a primary DNS record that was not proven to reach this server")
			}
		})
	}
}

func TestInitialSetupAcceptsPrimaryDNSMatchingOrigin(t *testing.T) {
	postCount := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-matching/dns_records":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"record-1","type":"A","name":"links.example.com","content":"8.8.8.8","proxied":true,"ttl":1}]}`))
		case r.Method == http.MethodPost:
			postCount++
			http.Error(w, `{"success":false}`, http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()
	oldBase := cloudflareAPIBaseURL
	cloudflareAPIBaseURL = mock.URL
	t.Cleanup(func() { cloudflareAPIBaseURL = oldBase })

	client := &cloudflareClient{
		apiToken:        "matching-setup-token",
		targetHost:      "links.example.com",
		primaryOriginIP: "8.8.8.8",
		http:            mock.Client(),
	}
	invalidateCache("zone-matching")
	record, created, err := client.ensureRecord("zone-matching", "links.example.com")
	if err != nil {
		t.Fatalf("matching primary DNS record was rejected: %v", err)
	}
	if created || record == nil || record.Content != "8.8.8.8" {
		t.Fatalf("record=%+v created=%v, want existing matching record", record, created)
	}
	if postCount != 0 {
		t.Fatalf("matching existing record triggered %d DNS writes", postCount)
	}
}
