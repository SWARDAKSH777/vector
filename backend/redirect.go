package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// handleAll is the catch-all handler. Priority order:
//  1. Subdomain link — Host header matches a subdomain-type link → redirect
//  2. Slug link — path matches a slug-type link → redirect
//  3. SPA route — known app path → serve index.html
//  4. Static asset → serve from embedded FS
//  5. 404 page
func (s *server) handleAll(webFS fs.FS, staticHandler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		host := strings.ToLower(stripHostPort(r.Host))
		isBaseHost, hostErr := s.isBaseLinkHost(host)
		if hostErr != nil {
			http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
			return
		}

		// Password unlock must be resolved against the current host so a link on
		// one managed domain cannot be unlocked or replayed through another.
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/unlock") {
			s.handlePasswordUnlock(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if !isBaseHost {
			parts := strings.SplitN(host, ".", 2)
			if len(parts) == 2 && path == "/" {
				var id int64
				var dest, status string
				var passwordHash sql.NullString
				var expiresAt sql.NullTime
				var maxClicks sql.NullInt64
				err := s.db.QueryRow(`SELECT id,destination_url,status,password_hash,expires_at,max_clicks FROM links
					WHERE short_code=? COLLATE BINARY AND domain=? COLLATE NOCASE AND redirect_type='subdomain'`, parts[0], parts[1]).Scan(&id, &dest, &status, &passwordHash, &expiresAt, &maxClicks)
				if err == nil {
					s.enforceAndRedirect(w, r, id, dest, status, passwordHash, expiresAt, maxClicks, parts[0])
					return
				}
				if !errors.Is(err, sql.ErrNoRows) {
					http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
					return
				}
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(notFoundPageHTML("this host")))
			return
		}

		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			staticHandler.ServeHTTP(w, r)
			return
		}
		if path == "/favicon.ico" {
			w.Header().Set("Cache-Control", "public, max-age=86400")
			staticHandler.ServeHTTP(w, r)
			return
		}
		trimmedPath := strings.Trim(path, "/")
		if strings.Contains(trimmedPath, "/") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(notFoundPageHTML(trimmedPath)))
			return
		}
		code := trimmedPath
		if reservedCodes[strings.ToLower(code)] {
			serveIndex(w, r, webFS)
			return
		}
		if code != "" {
			var id int64
			var dest, status string
			var passwordHash sql.NullString
			var expiresAt sql.NullTime
			var maxClicks sql.NullInt64
			err := s.db.QueryRow(`SELECT id,destination_url,status,password_hash,expires_at,max_clicks FROM links
				WHERE short_code=? COLLATE BINARY AND domain=? COLLATE NOCASE AND redirect_type='slug'`, code, host).Scan(&id, &dest, &status, &passwordHash, &expiresAt, &maxClicks)
			if err == nil {
				s.enforceAndRedirect(w, r, id, dest, status, passwordHash, expiresAt, maxClicks, code)
				return
			}
			if !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(notFoundPageHTML(code)))
	}
}

func (s *server) registeredDomain(host string) (bool, error) {
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM domains WHERE hostname=? COLLATE NOCASE AND status='active')`, host).Scan(&exists)
	return exists == 1, err
}

func (s *server) isBaseLinkHost(host string) (bool, error) {
	setupComplete, err := s.getConfigE("setup_complete")
	if err != nil {
		return false, err
	}
	if setupComplete != "true" || host == "localhost" || net.ParseIP(host) != nil {
		return true, nil
	}
	return s.registeredDomain(host)
}

// writeCurrentLinkUnavailable re-checks mutable availability state at the
// point of redirect. This closes races between the initial link lookup and the
// click update, and is also used by password-unlock POST requests which do not
// pass through the normal GET lookup path.
func writeDynamicLinkPage(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (s *server) writeCurrentLinkUnavailable(w http.ResponseWriter, id int64) bool {
	var status string
	var expiresAt sql.NullTime
	var maxClicks sql.NullInt64
	var lifetimeClicks int64
	err := s.db.QueryRow(`SELECT status,expires_at,max_clicks,lifetime_click_count FROM links WHERE id=?`, id).
		Scan(&status, &expiresAt, &maxClicks, &lifetimeClicks)
	if errors.Is(err, sql.ErrNoRows) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(notFoundPageHTML("link")))
		return true
	}
	if err != nil {
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return true
	}
	now := time.Now().UTC()
	if status == "expired" || (expiresAt.Valid && !now.Before(expiresAt.Time.UTC())) {
		_, _ = s.db.Exec(`UPDATE links SET status='expired' WHERE id=? AND status='active'`, id)
		writeDynamicLinkPage(w, http.StatusGone, expiredPageHTML)
		return true
	}
	if status != "active" {
		writeDynamicLinkPage(w, http.StatusNotFound, pausedPageHTML)
		return true
	}
	if maxClicks.Valid && maxClicks.Int64 > 0 && lifetimeClicks >= maxClicks.Int64 {
		_, _ = s.db.Exec(`UPDATE links SET status='paused' WHERE id=? AND status='active'`, id)
		writeDynamicLinkPage(w, http.StatusGone, clickLimitPageHTML)
		return true
	}
	return false
}

// enforceAndRedirect applies all link rules then redirects.
func (s *server) enforceAndRedirect(w http.ResponseWriter, r *http.Request, id int64, dest, status string,
	passwordHash sql.NullString, expiresAt sql.NullTime, maxClicks sql.NullInt64, code string) {
	if expiresAt.Valid && time.Now().After(expiresAt.Time) {
		_, _ = s.db.Exec(`UPDATE links SET status='expired' WHERE id=?`, id)
		writeDynamicLinkPage(w, http.StatusGone, expiredPageHTML)
		return
	}
	if status == "expired" {
		writeDynamicLinkPage(w, http.StatusGone, expiredPageHTML)
		return
	}
	if status != "active" {
		writeDynamicLinkPage(w, http.StatusNotFound, pausedPageHTML)
		return
	}
	if maxClicks.Valid && maxClicks.Int64 > 0 {
		var cc int64
		if err := s.db.QueryRow(`SELECT lifetime_click_count FROM links WHERE id=?`, id).Scan(&cc); err != nil {
			http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
			return
		}
		if cc >= maxClicks.Int64 {
			_, _ = s.db.Exec(`UPDATE links SET status='paused' WHERE id=?`, id)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(clickLimitPageHTML))
			return
		}
	}
	if passwordHash.Valid && passwordHash.String != "" {
		if cookie, err := r.Cookie(unlockCookieName(id)); err == nil && s.verifyUnlockToken(cookie.Value, id, passwordHash.String) {
			s.logClickAndRedirect(w, r, id, dest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(passwordPromptHTML(code, "")))
		return
	}
	s.logClickAndRedirect(w, r, id, dest)
}

var (
	unlockLimiter   = newRateLimiter(1.0/20.0, 8)
	unlockIPLimiter = newRateLimiter(1.0/5.0, 20)
)

func unlockCookieName(id int64) string { return "vector_unlock_" + strconv.FormatInt(id, 10) }

func (s *server) makeUnlockToken(id int64, passwordHash string, expiry time.Time) string {
	exp := strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, s.masterKey)
	_, _ = mac.Write([]byte(fmt.Sprintf("unlock:%d:%s:%s", id, passwordHash, exp)))
	return exp + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *server) verifyUnlockToken(token string, id int64, passwordHash string) bool {
	exp, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > n {
		return false
	}
	want := s.makeUnlockToken(id, passwordHash, time.Unix(n, 0))
	_, wantSig, _ := strings.Cut(want, ".")
	return subtle.ConstantTimeCompare([]byte(sig), []byte(wantSig)) == 1
}

func (s *server) handlePasswordUnlock(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 || parts[1] != "unlock" {
		http.NotFound(w, r)
		return
	}
	code := parts[0]
	host := strings.ToLower(stripHostPort(r.Host))
	var id int64
	var dest, hash string
	var err error
	isBaseHost, hostErr := s.isBaseLinkHost(host)
	if hostErr != nil {
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	if isBaseHost {
		err = s.db.QueryRow(`SELECT id,destination_url,password_hash FROM links
			WHERE short_code=? COLLATE BINARY AND domain=? COLLATE NOCASE AND redirect_type='slug' AND password_hash IS NOT NULL`, code, host).Scan(&id, &dest, &hash)
	} else if hostParts := strings.SplitN(host, ".", 2); len(hostParts) == 2 && hostParts[0] == strings.ToLower(code) {
		err = s.db.QueryRow(`SELECT id,destination_url,password_hash FROM links
			WHERE short_code=? COLLATE BINARY AND domain=? COLLATE NOCASE AND redirect_type='subdomain' AND password_hash IS NOT NULL`, code, hostParts[1]).Scan(&id, &dest, &hash)
	} else {
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.writeCurrentLinkUnavailable(w, id) {
		return
	}
	clientIP := requestClientIP(r)
	key := clientIP + "|" + strconv.FormatInt(id, 10)
	if !unlockIPLimiter.allow(clientIP) || !unlockLimiter.allow(key) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	mediaType, _, parseTypeErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if parseTypeErr != nil || !strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		http.Error(w, "content type must be application/x-www-form-urlencoded", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	// Never read credentials from the URL query string: URLs are routinely
	// retained in browser history, reverse-proxy logs and analytics systems.
	attempt := r.PostForm.Get("password")
	if len(attempt) > maximumPasswordBytes || !verifyPassword(attempt, hash) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(passwordPromptHTML(code, "Incorrect password, please try again.")))
		return
	}
	expiry := time.Now().Add(time.Hour)
	http.SetCookie(w, &http.Cookie{Name: unlockCookieName(id), Value: s.makeUnlockToken(id, hash, expiry), Path: "/", HttpOnly: true, Secure: requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: 3600})
	s.logClickAndRedirectMode(w, r, id, dest, true)
}

func (s *server) logClickAndRedirect(w http.ResponseWriter, r *http.Request, linkID int64, dest string) {
	s.logClickAndRedirectMode(w, r, linkID, dest, false)
}

func (s *server) logClickAndRedirectMode(w http.ResponseWriter, r *http.Request, linkID int64, dest string, forceCount bool) {
	var utmSource, utmMedium, utmCampaign string
	if err := s.db.QueryRow(`SELECT COALESCE(utm_source,''),COALESCE(utm_medium,''),COALESCE(utm_campaign,'') FROM links WHERE id=?`, linkID).Scan(&utmSource, &utmMedium, &utmCampaign); err != nil {
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	dest = appendUTMParameters(dest, utmSource, utmMedium, utmCampaign)
	if !forceCount && !shouldCountClick(r) {
		if s.writeCurrentLinkUnavailable(w, linkID) {
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}

	now := time.Now().UTC()
	capture := s.prepareAnalyticsCapture(r)
	tx, err := s.db.Begin()
	if err != nil {
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	res, err := tx.Exec(`UPDATE links SET click_count=click_count+1, lifetime_click_count=lifetime_click_count+1
		WHERE id=? AND status='active'
		  AND (expires_at IS NULL OR expires_at>?)
		  AND (max_clicks IS NULL OR lifetime_click_count<max_clicks)`, linkID, now)
	if err != nil {
		_ = tx.Rollback()
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	if n == 0 {
		_ = tx.Rollback()
		if !s.writeCurrentLinkUnavailable(w, linkID) {
			http.Error(w, "redirect state changed; retry", http.StatusConflict)
		}
		return
	}
	bucket := now.Truncate(time.Hour)
	if _, err := tx.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count) VALUES(?,?,1)
		ON CONFLICT(link_id,bucket_hour) DO UPDATE SET click_count=click_count+1`, linkID, bucket); err != nil {
		_ = tx.Rollback()
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	eventID := s.insertAnalyticsEvent(tx, linkID, now, capture)
	if err := tx.Commit(); err != nil {
		http.Error(w, "redirect unavailable", http.StatusServiceUnavailable)
		return
	}
	if eventID > 0 && !capture.CountryKnown && capture.ClientIP != "" && s.geo != nil {
		s.geo.enqueue(eventID, capture.ClientIP)
	}
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.Redirect(w, r, dest, http.StatusFound)
}

func shouldCountClick(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	// Block prefetch and prerender — check all known header variants.
	// Some mobile browsers (Chrome on Android, iOS Safari) send prefetch
	// requests without always setting Sec-Purpose, so we check all headers.
	purpose := strings.ToLower(strings.TrimSpace(
		r.Header.Get("Purpose") + " " +
			r.Header.Get("Sec-Purpose") + " " +
			r.Header.Get("X-Purpose") + " " +
			r.Header.Get("X-Moz"),
	))
	if strings.Contains(purpose, "prefetch") || strings.Contains(purpose, "prerender") || strings.Contains(purpose, "preview") {
		return false
	}

	// Range requests are often speculative media pre-loading, not user clicks.
	if r.Header.Get("Range") != "" {
		return false
	}

	ua := strings.ToLower(r.UserAgent())
	for _, marker := range []string{
		"googlebot", "bingbot", "yandexbot", "baiduspider", "duckduckbot",
		"facebookexternalhit", "facebot", "twitterbot", "linkedinbot",
		"slackbot", "discordbot", "telegrambot", "whatsapp", "skypeuripreview",
		// Additional bots/crawlers
		"applebot", "petalbot", "semrushbot", "ahrefsbot", "mj12bot",
		"dotbot", "rogerbot", "screaming frog",
	} {
		if strings.Contains(ua, marker) {
			return false
		}
	}
	return true
}

func appendUTMParameters(destination, source, medium, campaign string) string {
	if source == "" && medium == "" && campaign == "" {
		return destination
	}
	u, err := url.Parse(destination)
	if err != nil {
		return destination
	}
	query := u.Query()
	if source != "" {
		query.Set("utm_source", source)
	}
	if medium != "" {
		query.Set("utm_medium", medium)
	}
	if campaign != "" {
		query.Set("utm_campaign", campaign)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func referrerOrigin(raw string) string {
	if raw == "" {
		return ""
	}
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	// If no host is present but there's a scheme-less relative URL, it's a
	// same-origin referrer — treat as direct (no external referrer info).
	if u.Host == "" {
		return ""
	}
	// Require a recognised web scheme.
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	origin := scheme + "://" + strings.ToLower(u.Host)
	if len(origin) > 512 {
		return ""
	}
	return origin
}

func serveIndex(w http.ResponseWriter, r *http.Request, webFS fs.FS) {
	data, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// ---- Error pages ----

func notFoundPageHTML(code string) string {
	c := code
	if c == "" {
		c = "this link"
	}
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Link not found</title>` + basePageStyle + `</head>
<body><div class="box"><div class="icon">🔍</div><h1>Link not found</h1>
<p>The short link <code>` + html.EscapeString(c) + `</code> doesn't exist or has been removed.</p>
<a href="/" class="btn">Go to homepage</a></div></body></html>`
}

const expiredPageHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Link expired</title>` + basePageStyle + `</head>
<body><div class="box"><div class="icon">⏰</div><h1>This link has expired</h1>
<p>The short link you followed is no longer active.</p></div></body></html>`

const pausedPageHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Link unavailable</title>` + basePageStyle + `</head>
<body><div class="box"><div class="icon">⏸</div><h1>This link is paused</h1>
<p>The owner has temporarily disabled this short link.</p></div></body></html>`

const clickLimitPageHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Limit reached</title>` + basePageStyle + `</head>
<body><div class="box"><div class="icon">🚫</div><h1>Click limit reached</h1>
<p>This short link has reached its maximum number of clicks.</p></div></body></html>`

const basePageStyle = `<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;background:#fafafa;color:#171717;padding:1rem}
.box{text-align:center;max-width:380px}
.icon{font-size:3rem;margin-bottom:1rem}
h1{font-size:1.4rem;font-weight:700;margin-bottom:.5rem}
p{color:#737373;font-size:.95rem;line-height:1.5;margin-bottom:1.5rem}
code{background:#f0f0f0;padding:.15em .4em;border-radius:.3em;font-size:.9em}
.btn{display:inline-block;padding:.6em 1.4em;background:#171717;color:#fff;border-radius:.6em;text-decoration:none;font-size:.9rem;font-weight:600}
</style>`

func passwordPromptHTML(code, errMsg string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<p style="color:#dc2626;font-size:.85rem;margin:0 0 12px">` + html.EscapeString(errMsg) + `</p>`
	}
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Password required</title>` + basePageStyle + `
<style>input{width:100%;padding:10px;border:1px solid #e5e5e5;border-radius:8px;font-size:.9rem;margin-bottom:10px}
.btn{width:100%;border:none;cursor:pointer;padding:10px}</style>
</head><body><div class="box"><div class="icon">🔒</div><h1>Password required</h1>
<p>This link is protected.</p>` + errHTML + `
<form method="POST" action="/` + html.EscapeString(code) + `/unlock">
<input type="password" name="password" placeholder="Enter password" autofocus/>
<button type="submit" class="btn">Continue →</button>
</form></div></body></html>`
}
