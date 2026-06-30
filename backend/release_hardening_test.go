package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"text/template"
)

func TestRandomCodeFailsClosedWhenEntropyUnavailable(t *testing.T) {
	original := cryptoRandRead
	cryptoRandRead = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	t.Cleanup(func() { cryptoRandRead = original })

	if _, err := randomCode(8); err == nil {
		t.Fatal("randomCode succeeded without cryptographic entropy")
	}
}

func TestGeneratedNginxTLSConfigUsesHardenedProxyHeadersAndHostGuard(t *testing.T) {
	tmpl, err := template.New("nginx").Parse(nginxSSLTemplate)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	data := nginxData{
		Domain:      "example.com",
		Port:        "8081",
		Wildcard:    true,
		HostPattern: nginxHostPattern("example.com", true),
	}
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	conf := out.String()
	for _, required := range []string{
		"server_name example.com *.example.com;",
		"return 444;",
		"proxy_pass http://127.0.0.1:8081;",
		"proxy_set_header X-Forwarded-For $vector_client_ip;",
		"proxy_set_header X-Vector-Cloudflare-Trusted $vector_from_cloudflare;",
		"proxy_set_header X-Vector-Country $vector_country_code;",
		"ssl_protocols TLSv1.2 TLSv1.3;",
		"ssl_session_tickets off;",
	} {
		if !strings.Contains(conf, required) {
			t.Fatalf("generated config missing %q:\n%s", required, conf)
		}
	}
	for _, forbidden := range []string{
		"$proxy_add_x_forwarded_for",
		"http2 on;",
	} {
		if strings.Contains(conf, forbidden) {
			t.Fatalf("generated config contains forbidden directive %q", forbidden)
		}
	}
}

func TestNginxHostPatternDoesNotMatchNestedOrLookalikeDomains(t *testing.T) {
	pattern := nginxHostPattern("example.com", true)
	if pattern != `(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)?example\.com` {
		t.Fatalf("unexpected host pattern %q", pattern)
	}
}

func TestPrivilegedHelperOnlyAcceptsConfiguredBackendPort(t *testing.T) {
	t.Setenv("VECTOR_INTERNAL_PORT", "8081")
	if !validInternalPort("8081") {
		t.Fatal("configured backend port was rejected")
	}
	for _, value := range []string{"8080", "8082", "22", "65536", "not-a-port"} {
		if validInternalPort(value) {
			t.Fatalf("unexpected privileged-helper port accepted: %q", value)
		}
	}
}
