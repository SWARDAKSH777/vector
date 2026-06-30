package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		csp := "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; manifest-src 'self'"
		if requestUsesHTTPS(r) {
			csp += "; upgrade-insecure-requests"
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		w.Header().Set("Content-Security-Policy", csp)
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/setup" || r.URL.Path == "/login" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

type rateLimiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	rate        float64
	capacity    float64
	lastCleanup time.Time
	maxBuckets  int
}

func newRateLimiter(rps float64, capacity int) *rateLimiter {
	return newRateLimiterWithLimit(rps, capacity, 4096)
}

func newRateLimiterWithLimit(rps float64, capacity, maxBuckets int) *rateLimiter {
	if maxBuckets < 64 {
		maxBuckets = 64
	}
	return &rateLimiter{
		buckets:     make(map[string]*bucket),
		rate:        rps,
		capacity:    float64(capacity),
		lastCleanup: time.Now(),
		maxBuckets:  maxBuckets,
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if now.Sub(rl.lastCleanup) >= time.Minute {
		cutoff := now.Add(-15 * time.Minute)
		for bucketKey, item := range rl.buckets {
			if item.lastSeen.Before(cutoff) {
				delete(rl.buckets, bucketKey)
			}
		}
		rl.lastCleanup = now
	}
	b, ok := rl.buckets[key]
	if !ok {
		if len(rl.buckets) >= rl.maxBuckets {
			var oldestKey string
			var oldest time.Time
			for candidate, item := range rl.buckets {
				if oldestKey == "" || item.lastSeen.Before(oldest) {
					oldestKey, oldest = candidate, item.lastSeen
				}
			}
			if oldestKey != "" {
				delete(rl.buckets, oldestKey)
			}
		}
		b = &bucket{tokens: rl.capacity, lastSeen: now}
		rl.buckets[key] = b
	}
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens = minF(b.tokens+elapsed*rl.rate, rl.capacity)
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

const (
	vectorCloudflareTrustedHeader = "X-Vector-Cloudflare-Trusted"
	vectorCountryHeader           = "X-Vector-Country"
)

// analyticsClientIP always consumes the address normalized by Vector's trusted
// Nginx boundary. Nginx selects CF-Connecting-IP only when the TCP peer belongs
// to Cloudflare's published networks; direct clients cannot opt themselves in.
func analyticsClientIP(r *http.Request) string {
	return requestClientIP(r)
}

// trustedCloudflareCountry returns Cloudflare's visitor country only when the
// request crossed the authenticated local Nginx proxy and Nginx independently
// proved that its public-side TCP peer was a Cloudflare address. The original
// CF-IPCountry header is never read directly by the application.
func trustedCloudflareCountry(r *http.Request) (string, bool) {
	if r == nil || !trustedProxyRequest(r) || strings.TrimSpace(r.Header.Get(vectorCloudflareTrustedHeader)) != "1" {
		return "", false
	}
	code := strings.ToUpper(strings.TrimSpace(r.Header.Get(vectorCountryHeader)))
	if !validCountryCode(code) || code == "XX" || code == "T1" {
		return "", false
	}
	return code, true
}

func requestClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.Trim(r.RemoteAddr, "[]")
	}
	remoteIP := net.ParseIP(host)
	if remoteIP != nil && trustedProxyRequest(r) {
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
			if ip := net.ParseIP(strings.Trim(first, "[]")); ip != nil {
				return ip.String()
			}
		}
	}
	if remoteIP != nil {
		return remoteIP.String()
	}
	return host
}

func (s *server) restrictLinkSubdomainSurface(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setupComplete, err := s.getConfigE("setup_complete")
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "security configuration is temporarily unavailable")
			return
		}
		if setupComplete != "true" {
			next.ServeHTTP(w, r)
			return
		}
		host := strings.ToLower(stripHostPort(r.Host))
		if host == "localhost" || net.ParseIP(host) != nil {
			if r.URL.Path == "/healthz" && isLoopbackRemote(r.RemoteAddr) {
				next.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/login" || r.URL.Path == "/setup" {
				writeErr(w, http.StatusMisdirectedRequest, "administrator interface is available only through the configured HTTPS domain")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		registered, err := s.registeredDomain(host)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "domain routing is temporarily unavailable")
			return
		}
		if registered {
			// Keep the administrator SPA and API pinned to the installation's
			// configured primary origin. The default link domain is a content
			// preference and may be changed at any time; using it as the admin
			// origin would strand host-only session cookies on the old hostname.
			adminDomain, err := s.primaryAdminDomainE()
			if err != nil {
				writeErr(w, http.StatusServiceUnavailable, "domain routing is temporarily unavailable")
				return
			}
			if strings.EqualFold(host, adminDomain) {
				if isAllowedSlugSurfaceRequest(r) {
					next.ServeHTTP(w, r)
					return
				}
				if r.TLS == nil && !trustedProxyRequest(r) {
					writeErr(w, http.StatusMisdirectedRequest, "administrator interface requires the trusted reverse proxy")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if isAllowedSlugSurfaceRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}

		// Wildcard hosts are public subdomain redirect surfaces only. Never
		// expose the dashboard, setup, static UI, or administrator APIs there.
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/unlock") {
			code := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/unlock")
			parts := strings.SplitN(host, ".", 2)
			if len(parts) == 2 && code != "" && strings.EqualFold(parts[0], code) {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

func (s *server) defaultActiveDomainE() (string, error) {
	var hostname string
	err := s.db.QueryRow(`SELECT hostname FROM domains WHERE status='active' AND is_default=1 ORDER BY id LIMIT 1`).Scan(&hostname)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if hostname == "" {
		configuredValue, err := s.getConfigE("domain")
		if err != nil {
			return "", err
		}
		configured := strings.ToLower(strings.TrimSpace(configuredValue))
		if configured != "" {
			err = s.db.QueryRow(`SELECT hostname FROM domains WHERE status='active' AND lower(hostname)=lower(?) ORDER BY id LIMIT 1`, configured).Scan(&hostname)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
		}
	}
	return strings.ToLower(hostname), nil
}

func (s *server) defaultActiveDomain() string {
	domain, _ := s.defaultActiveDomainE()
	return domain
}

// primaryAdminDomain returns the configured installation origin when it is
// still an active registered domain. It falls back to the active link default
// only to keep an older or partially migrated installation recoverable.
func (s *server) primaryAdminDomainE() (string, error) {
	configuredValue, err := s.getConfigE("domain")
	if err != nil {
		return "", err
	}
	configured := strings.ToLower(strings.TrimSpace(configuredValue))
	if configured != "" {
		var active int
		if err := s.db.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM domains WHERE hostname=? COLLATE NOCASE AND status='active'
		)`, configured).Scan(&active); err != nil {
			return "", err
		} else if active == 1 {
			return configured, nil
		}
	}
	return s.defaultActiveDomainE()
}

func (s *server) primaryAdminDomain() string {
	domain, _ := s.primaryAdminDomainE()
	return domain
}

func isAllowedSlugSurfaceRequest(r *http.Request) bool {
	path := strings.Trim(r.URL.Path, "/")
	if path == "" {
		return false
	}
	parts := strings.Split(path, "/")
	if r.Method == http.MethodPost {
		return len(parts) == 2 && parts[1] == "unlock" && parts[0] != "" && !reservedCodes[strings.ToLower(parts[0])]
	}
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && len(parts) == 1 && !reservedCodes[strings.ToLower(parts[0])]
}

func (s *server) blockDirectIPAfterSetup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") || r.URL.Path == "/setup" {
			next.ServeHTTP(w, r)
			return
		}
		setupComplete, err := s.getConfigE("setup_complete")
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "security configuration is temporarily unavailable")
			return
		}
		domain, err := s.primaryAdminDomainE()
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "domain routing is temporarily unavailable")
			return
		}
		if setupComplete == "true" && domain != "" {
			host := stripHostPort(r.Host)
			if net.ParseIP(host) != nil {
				http.Redirect(w, r, "https://"+domain+r.URL.RequestURI(), http.StatusPermanentRedirect)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(strings.TrimSpace(host), "[]")
}

func (s *server) setupGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") || r.URL.Path == "/setup" || r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		setupComplete, err := s.getConfigE("setup_complete")
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "setup state is temporarily unavailable")
			return
		}
		if setupComplete != "true" {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func csrfMAC(key []byte, source string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("csrf:" + source))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *server) csrfSource(r *http.Request) string {
	// During initial setup, a valid bootstrap session must take precedence over
	// stale administrator cookies left by an earlier failed or reinstalled
	// setup. Otherwise an obsolete vector_setup_session cookie can shadow the
	// valid bootstrap cookie and make the next setup request fail with 401.
	//
	// A database outage here must not change which CSRF source is selected
	// unpredictably; if status cannot be determined, fall through to the
	// normal session-cookie selection below rather than trusting bootstrap.
	if setupComplete, err := s.getConfigE("setup_complete"); err == nil && setupComplete != "true" {
		if c, cookieErr := r.Cookie(bootstrapCookieName); cookieErr == nil && c.Value != "" {
			if authenticated, ok := s.bootstrapAuthenticated(r); ok && authenticated {
				return bootstrapCookieName + ":" + tokenDigest(c.Value)
			}
		}
	}

	// Select the first valid administrator session rather than merely the first
	// cookie present. Browsers can retain an expired __Host or setup cookie while
	// also carrying a newer valid session.
	for _, name := range []string{secureSessionCookieName, setupSessionCookieName} {
		c, err := r.Cookie(name)
		if err != nil || c.Value == "" {
			continue
		}
		if _, authErr := s.authenticatedUserToken(r, c.Value); authErr == nil {
			return name + ":" + tokenDigest(c.Value)
		}
	}
	return ""
}

func (s *server) handleCSRF(w http.ResponseWriter, r *http.Request) {
	source := s.csrfSource(r)
	if source == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": csrfMAC(s.masterKey, source)})
}

func (s *server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// These endpoints establish authentication and cannot possess a CSRF token yet.
		// The setup domain check is read-only and remains protected by the bootstrap
		// session, so it does not need a second CSRF-token round trip.
		if r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/setup/bootstrap/login" || r.URL.Path == "/api/setup/check-domain" {
			if !sameOriginRequest(r) {
				writeErr(w, http.StatusForbidden, "cross-origin login request rejected")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !sameOriginRequest(r) {
			writeErr(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		source := s.csrfSource(r)
		got := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		want := csrfMAC(s.masterKey, source)
		if source == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeErr(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if origin == "" {
		// Non-browser/API clients still need the unguessable CSRF token.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	originScheme := strings.ToLower(u.Scheme)
	if originScheme != "http" && originScheme != "https" {
		return false
	}
	requestScheme := "http"
	if requestUsesHTTPS(r) {
		requestScheme = "https"
	}
	if originScheme != requestScheme {
		return false
	}
	originAuthority, ok := canonicalOriginAuthority(originScheme, u.Host)
	if !ok {
		return false
	}
	requestAuthority, ok := canonicalOriginAuthority(requestScheme, r.Host)
	return ok && strings.EqualFold(originAuthority, requestAuthority)
}

func canonicalOriginAuthority(scheme, authority string) (string, bool) {
	u, err := url.Parse("//" + strings.TrimSpace(authority))
	if err != nil || u.User != nil || u.Hostname() == "" {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	port := u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", false
		}
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}
