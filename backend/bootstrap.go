package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	bootstrapCookieName = "vector_bootstrap_v1"
	bootstrapSessionTTL = time.Hour
)

type bootstrapCredential struct {
	Username     string
	Salt         string
	PasswordHash string
}

type bootstrapClaims struct {
	Exp      int64  `json:"exp"`
	Verifier string `json:"verifier"`
}

func bootstrapCredentialPath() string {
	dataDir := getenv("DATA_DIR", "/opt/vector/data")
	return filepath.Join(dataDir, "bootstrap.conf")
}

func loadBootstrapCredential() (*bootstrapCredential, error) {
	f, err := os.Open(bootstrapCredentialPath())
	if err != nil {
		return nil, err
	}
	defer f.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("invalid bootstrap credential file")
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	cred := &bootstrapCredential{
		Username:     values["username"],
		Salt:         values["salt"],
		PasswordHash: values["password_hash"],
	}
	if cred.PasswordHash == "" && values["password_sha256"] != "" {
		cred.PasswordHash = strings.ToLower(values["password_sha256"])
	}
	if cred.Username == "" || cred.PasswordHash == "" {
		return nil, errors.New("bootstrap credential file is incomplete")
	}
	if len(cred.Username) > 128 || len(cred.PasswordHash) > 512 {
		return nil, errors.New("bootstrap credential file is invalid")
	}
	if !strings.HasPrefix(cred.PasswordHash, "pbkdf2-sha256$") {
		if cred.Salt == "" || len(cred.Salt) > 128 || len(cred.PasswordHash) != 64 {
			return nil, errors.New("bootstrap credential file is invalid")
		}
		if _, err := hex.DecodeString(cred.PasswordHash); err != nil {
			return nil, errors.New("bootstrap credential hash is invalid")
		}
	}
	return cred, nil
}

func bootstrapPasswordHash(salt, password string) string {
	sum := sha256.Sum256([]byte(salt + ":" + password))
	return hex.EncodeToString(sum[:])
}

func verifyBootstrapCredential(cred *bootstrapCredential, username, password string) bool {
	if cred == nil {
		return false
	}
	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(cred.Username)) == 1
	passwordOK := false
	if strings.HasPrefix(cred.PasswordHash, "pbkdf2-sha256$") {
		passwordOK = verifyPassword(password, cred.PasswordHash)
	} else {
		got := bootstrapPasswordHash(cred.Salt, password)
		passwordOK = subtle.ConstantTimeCompare([]byte(got), []byte(cred.PasswordHash)) == 1
	}
	return usernameOK && passwordOK
}

func bootstrapVerifier(cred *bootstrapCredential) string {
	if cred == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(cred.Username + ":" + cred.Salt + ":" + cred.PasswordHash))
	return hex.EncodeToString(sum[:])
}

func signBootstrapToken(secret []byte, verifier string, expiry time.Time) string {
	payload, _ := json.Marshal(bootstrapClaims{Exp: expiry.Unix(), Verifier: verifier})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("bootstrap:" + encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig
}

func parseBootstrapToken(secret []byte, token string) (*bootstrapClaims, error) {
	idx := strings.LastIndexByte(token, '.')
	if idx <= 0 || idx == len(token)-1 {
		return nil, errors.New("malformed bootstrap token")
	}
	encoded, sig := token[:idx], token[idx+1:]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("bootstrap:" + encoded))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return nil, errors.New("invalid bootstrap signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var claims bootstrapClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if claims.Exp <= time.Now().Unix() {
		return nil, errors.New("bootstrap session expired")
	}
	if claims.Verifier == "" {
		return nil, errors.New("bootstrap verifier missing")
	}
	return &claims, nil
}

func setBootstrapCookie(w http.ResponseWriter, secret []byte, cred *bootstrapCredential, secure bool) {
	token := signBootstrapToken(secret, bootstrapVerifier(cred), time.Now().Add(bootstrapSessionTTL))
	http.SetCookie(w, &http.Cookie{
		Name:     bootstrapCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(bootstrapSessionTTL.Seconds()),
	})
}

func clearBootstrapCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     bootstrapCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// bootstrapAuthenticated reports whether the request carries a valid bootstrap
// session. The second return value distinguishes a definitive "not
// authenticated" answer from "could not determine" (database outage):
// callers must fail closed on the latter rather than treat it as open setup.
func (s *server) bootstrapAuthenticated(r *http.Request) (authenticated bool, ok bool) {
	setupComplete, err := s.getConfigE("setup_complete")
	if err != nil {
		return false, false
	}
	if setupComplete == "true" {
		return false, true
	}
	cred, err := loadBootstrapCredential()
	if err != nil {
		return false, true
	}
	cookie, err := r.Cookie(bootstrapCookieName)
	if err != nil {
		return false, true
	}
	claims, err := parseBootstrapToken(s.masterKey, cookie.Value)
	if err != nil {
		return false, true
	}
	return subtle.ConstantTimeCompare([]byte(claims.Verifier), []byte(bootstrapVerifier(cred))) == 1, true
}

var bootstrapLimiter = newRateLimiter(1.0/12.0, 5)

func (s *server) handleBootstrapLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	setupComplete, err := s.getConfigE("setup_complete")
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "setup status is temporarily unavailable")
		return
	}
	if setupComplete == "true" {
		writeErr(w, http.StatusForbidden, "setup already completed")
		return
	}
	if !bootstrapLimiter.allow(requestClientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many bootstrap login attempts; try again shortly")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Username) > 128 || len(req.Password) > maximumPasswordBytes {
		writeErr(w, http.StatusUnauthorized, "invalid bootstrap username or password")
		return
	}
	cred, err := loadBootstrapCredential()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "bootstrap credentials are unavailable; rerun the installer or run vector-bootstrap-reset as root")
		return
	}
	if !verifyBootstrapCredential(cred, strings.TrimSpace(req.Username), req.Password) {
		writeErr(w, http.StatusUnauthorized, "invalid bootstrap username or password")
		return
	}

	setBootstrapCookie(w, s.masterKey, cred, requestUsesHTTPS(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expires_in": int(bootstrapSessionTTL.Seconds())})
}

func (s *server) handleBootstrapLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	clearBootstrapCookie(w, requestUsesHTTPS(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) requireBootstrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setupComplete, err := s.getConfigE("setup_complete")
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "setup status is temporarily unavailable")
			return
		}
		if setupComplete == "true" {
			writeErr(w, http.StatusForbidden, "setup already completed")
			return
		}
		authenticated, ok := s.bootstrapAuthenticated(r)
		if !ok {
			writeErr(w, http.StatusServiceUnavailable, "bootstrap authentication is temporarily unavailable")
			return
		}
		if !authenticated {
			writeErr(w, http.StatusUnauthorized, "bootstrap authentication required")
			return
		}
		next(w, r)
	}
}

func (s *server) consumeBootstrap(w http.ResponseWriter, r *http.Request) {
	clearBootstrapCookie(w, requestUsesHTTPS(r))
}

func bootstrapStatus(s *server, r *http.Request) (required, authenticated, available bool, message string) {
	setupComplete, err := s.getConfigE("setup_complete")
	if err != nil {
		return false, false, false, "setup status is temporarily unavailable"
	}
	if setupComplete == "true" {
		return false, false, true, ""
	}
	if _, err := loadBootstrapCredential(); err != nil {
		// Do not expose filesystem paths or parser details through an unauthenticated
		// setup-status endpoint. The installer/reset command is the actionable fix.
		return true, false, false, "bootstrap credentials are unavailable; rerun the installer or run vector-bootstrap-reset as root"
	}
	auth, ok := s.bootstrapAuthenticated(r)
	if !ok {
		return true, false, false, "bootstrap authentication is temporarily unavailable"
	}
	return true, auth, true, ""
}
