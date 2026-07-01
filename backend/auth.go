package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// Algorithm constants
// ---------------------------------------------------------------------------

const (
	// Argon2id parameters (RFC 9106 "first recommended option", tuned for
	// ≈100 ms on a single core of a modest VPS).
	argon2idTime    uint32 = 3
	argon2idMemory  uint32 = 64 * 1024 // 64 MiB
	argon2idThreads uint8  = 2
	argon2idKeyLen  uint32 = 32
	argon2idSaltLen        = 16

	// bcrypt cost — 12 is the widely accepted production baseline (≈250 ms).
	// Used as a fallback when the operator prefers bcrypt over argon2id.
	bcryptCost = 12

	// PBKDF2 parameters — kept for verifying existing stored hashes and
	// migrating them to argon2id on next login. New hashes are never written
	// in PBKDF2 format.
	pbkdf2IterationsCurrent = 600_000
	pbkdf2IterationsLegacy  = 100_000

	// Session constants
	secureSessionCookieName = "__Host-vector_session"
	setupSessionCookieName  = "vector_setup_session"
	sessionAbsoluteTTL      = 24 * time.Hour
	sessionIdleTTL          = 2 * time.Hour
)

// ---------------------------------------------------------------------------
// Argon2id — primary algorithm for all new password hashes
// ---------------------------------------------------------------------------

// hashArgon2id produces an encoded argon2id hash in the format:
//
//	argon2id$v=19$t=3,m=65536,p=2$<base64-salt>$<base64-hash>
func hashArgon2id(password string) (string, error) {
	salt := make([]byte, argon2idSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: entropy read failed: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt,
		argon2idTime, argon2idMemory, argon2idThreads, argon2idKeyLen)
	encoded := fmt.Sprintf(
		"argon2id$v=%d$t=%d,m=%d,p=%d$%s$%s",
		argon2.Version,
		argon2idTime,
		argon2idMemory,
		argon2idThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// verifyArgon2id returns true when password matches an encoded argon2id hash.
// It re-derives the key using the parameters stored in the encoded string so
// older hashes with different parameters continue to verify correctly.
func verifyArgon2id(password, encoded string) bool {
	// format: argon2id$v=<ver>$t=<time>,m=<mem>,p=<threads>$<salt>$<hash>
	// There are exactly five fields. The previous six-field check rejected every
	// newly generated Argon2id password and made administrator/link login fail.
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	var ver, t, m, p int
	if _, err := fmt.Sscanf(parts[1], "v=%d", &ver); err != nil || ver != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[2], "t=%d,m=%d,p=%d", &t, &m, &p); err != nil {
		return false
	}
	// Stored hashes are normally trusted database state, but strict bounds stop a
	// damaged or attacker-modified database from forcing excessive CPU or memory.
	if t < 1 || t > 10 || m < 8*1024 || m > 1024*1024 || p < 1 || p > 32 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[3])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[4])
	if err1 != nil || err2 != nil || len(salt) < 8 || len(salt) > 64 || len(want) < 16 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------------------------------------------------------------------------
// bcrypt — available as an opt-in algorithm; verified but not produced by
// default (argon2id is preferred for new hashes).
// ---------------------------------------------------------------------------

// hashBcrypt produces a bcrypt hash using the configured cost.
// Retained for operators who explicitly prefer bcrypt. Not called by
// the default code path; call hashPasswordWithError instead.
func hashBcrypt(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(hash), nil
}

func verifyBcrypt(password, encoded string) bool {
	return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil
}

// ---------------------------------------------------------------------------
// PBKDF2-SHA256 — read-only; only used to verify existing stored hashes
// ---------------------------------------------------------------------------

// pbkdf2 is the pure-Go PBKDF2 implementation retained from the previous
// release so that stored PBKDF2 hashes can be verified without importing
// the x/crypto/pbkdf2 package.
func pbkdf2derive(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		_, _ = prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		_, _ = prf.Write(buf[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			prf.Reset()
			_, _ = prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

func verifyPBKDF2(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	var iter int
	var salt, want []byte
	switch {
	case len(parts) == 4 && parts[0] == "pbkdf2-sha256":
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 10_000 || n > 5_000_000 {
			return false
		}
		iter = n
		s, err1 := base64.RawStdEncoding.DecodeString(parts[2])
		w, err2 := base64.RawStdEncoding.DecodeString(parts[3])
		if err1 != nil || err2 != nil || len(s) < 16 || len(w) != 32 {
			return false
		}
		salt, want = s, w
	case len(parts) == 3 && parts[0] == "pbkdf2": // v5 legacy
		s, err1 := base64.RawStdEncoding.DecodeString(parts[1])
		w, err2 := base64.RawStdEncoding.DecodeString(parts[2])
		if err1 != nil || err2 != nil || len(s) < 16 || len(w) != 32 {
			return false
		}
		iter = pbkdf2IterationsLegacy
		salt, want = s, w
	default:
		return false
	}
	got := pbkdf2derive([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------------------------------------------------------------------------
// Unified hash / verify / upgrade API
// ---------------------------------------------------------------------------

// hashPasswordWithError produces a new argon2id hash. This is the only
// function that writes new password hashes; it always uses argon2id.
func hashPasswordWithError(password string) (string, error) {
	return hashArgon2id(password)
}

// hashPassword is used by call sites that cannot propagate errors (e.g.
// deterministic internal/test paths). HTTP handlers use hashPasswordWithError.
func hashPassword(password string) string {
	h, err := hashPasswordWithError(password)
	if err != nil {
		return ""
	}
	return h
}

// verifyPassword accepts argon2id, bcrypt ($2a/$2b/$2y prefix), and
// pbkdf2-sha256/pbkdf2 hashes transparently. The algorithm is detected
// from the encoded string prefix; no caller change is required.
func verifyPassword(password, encoded string) bool {
	switch {
	case strings.HasPrefix(encoded, "argon2id$"):
		return verifyArgon2id(password, encoded)
	case strings.HasPrefix(encoded, "$2a$") ||
		strings.HasPrefix(encoded, "$2b$") ||
		strings.HasPrefix(encoded, "$2y$"):
		return verifyBcrypt(password, encoded)
	default:
		return verifyPBKDF2(password, encoded)
	}
}

// passwordHashNeedsUpgrade returns true when the stored hash is not argon2id
// (the current preferred algorithm). On next successful login the hash is
// transparently re-hashed to argon2id and the stored value is updated.
func passwordHashNeedsUpgrade(encoded string) bool {
	if !strings.HasPrefix(encoded, "argon2id$") {
		return true
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 {
		return true
	}
	var ver, t, m, p int
	if _, err := fmt.Sscanf(parts[1], "v=%d", &ver); err != nil || ver != argon2.Version {
		return true
	}
	if _, err := fmt.Sscanf(parts[2], "t=%d,m=%d,p=%d", &t, &m, &p); err != nil {
		return true
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[3])
	hash, hashErr := base64.RawStdEncoding.DecodeString(parts[4])
	if saltErr != nil || hashErr != nil {
		return true
	}
	// Upgrade hashes that are weaker than any current dimension, while leaving
	// intentionally stronger operator-generated hashes intact.
	return t < int(argon2idTime) || m < int(argon2idMemory) || p < int(argon2idThreads) ||
		len(salt) < argon2idSaltLen || len(hash) < int(argon2idKeyLen)
}

// ---------------------------------------------------------------------------
// Dummy hash for timing-safe login (prevents user enumeration)
// ---------------------------------------------------------------------------

var (
	dummyHashOnce sync.Once
	dummyHash     string

	loginIPLimit   = newRateLimiter(1.0/45.0, 20)
	loginAcctLimit = newRateLimiter(1.0/180.0, 5)
)

// loginDummyHash returns a pre-computed argon2id hash of a never-used
// credential. Comparing against it when a user email is not found ensures
// that login response time is indistinguishable between existing and
// non-existing accounts.
func loginDummyHash() string {
	dummyHashOnce.Do(func() {
		h, err := hashArgon2id("vector-dummy-password-never-used-$9Kz!mQ2")
		if err != nil {
			// Fallback: bcrypt is fast enough for a one-time startup call.
			h, _ = hashBcrypt("vector-dummy-password-never-used-$9Kz!mQ2")
		}
		dummyHash = h
	})
	return dummyHash
}

// ---------------------------------------------------------------------------
// Session and cookie helpers
// ---------------------------------------------------------------------------

func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func userAgentDigest(ua string) string {
	sum := sha256.Sum256([]byte(ua))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func requestUsesHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !trustedProxyRequest(r) {
		return false
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if comma := strings.IndexByte(proto, ','); comma >= 0 {
		proto = proto[:comma]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *server) createSession(w http.ResponseWriter, r *http.Request, userID int64, setup bool) error {
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	expires := now.Add(sessionAbsoluteTTL)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO sessions
		(token_hash,user_id,created_at,last_seen_at,expires_at,user_agent_hash)
		VALUES (?,?,?,?,?,?)`,
		tokenDigest(token), userID, now, now, expires, userAgentDigest(r.UserAgent()),
	); err == nil {
		// Bound concurrent sessions to the ten newest. Keep insertion and cleanup
		// atomic so a partial database failure cannot silently defeat the limit.
		_, err = tx.Exec(`DELETE FROM sessions WHERE user_id=? AND token_hash NOT IN (
			SELECT token_hash FROM sessions WHERE user_id=? ORDER BY created_at DESC, token_hash DESC LIMIT 10
		)`, userID, userID)
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}

	secure := requestUsesHTTPS(r) && !setup
	name := setupSessionCookieName
	if secure {
		name = secureSessionCookieName
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionAbsoluteTTL.Seconds()),
	})
	return nil
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

func (s *server) clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, secureSessionCookieName, true)
	clearCookie(w, setupSessionCookieName, false)
}

func (s *server) deleteRequestSession(r *http.Request) {
	for _, name := range []string{secureSessionCookieName, setupSessionCookieName} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenDigest(c.Value))
		}
	}
}

// ---------------------------------------------------------------------------
// Session validation
// ---------------------------------------------------------------------------

var errSessionBackendUnavailable = errors.New("session store unavailable")

func (s *server) authenticatedUserToken(r *http.Request, token string) (int64, error) {
	if token == "" {
		return 0, errors.New("session missing")
	}
	var uid int64
	var lastSeen, expires time.Time
	var uaHash string
	err := s.db.QueryRow(`SELECT s.user_id, s.last_seen_at, s.expires_at, s.user_agent_hash
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND u.role IN ('system_admin','user') AND u.disabled=0 AND u.deleted_at IS NULL`,
		tokenDigest(token)).Scan(&uid, &lastSeen, &expires, &uaHash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("session invalid")
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errSessionBackendUnavailable, err)
	}
	now := time.Now().UTC()
	if now.After(expires) || now.Sub(lastSeen) > sessionIdleTTL {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenDigest(token))
		return 0, errors.New("session expired")
	}
	if uaHash != "" && subtle.ConstantTimeCompare(
		[]byte(uaHash), []byte(userAgentDigest(r.UserAgent()))) != 1 {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenDigest(token))
		return 0, errors.New("session client changed")
	}
	if now.Sub(lastSeen) > 5*time.Minute {
		_, _ = s.db.Exec(`UPDATE sessions SET last_seen_at=? WHERE token_hash=?`, now, tokenDigest(token))
	}
	return uid, nil
}

func (s *server) authenticatedUser(r *http.Request) (int64, error) {
	var lastErr error
	for _, name := range []string{secureSessionCookieName, setupSessionCookieName} {
		c, err := r.Cookie(name)
		if err != nil || c.Value == "" {
			continue
		}
		uid, authErr := s.authenticatedUserToken(r, c.Value)
		if authErr == nil {
			return uid, nil
		}
		lastErr = authErr
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, errors.New("session missing")
}

type ctxKey string

const ctxUserID ctxKey = "user_id"

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := s.authenticatedUser(r)
		if err != nil {
			w.Header().Set("Cache-Control", "no-store")
			if errors.Is(err, errSessionBackendUnavailable) {
				writeErr(w, http.StatusServiceUnavailable, "session verification is temporarily unavailable")
				return
			}
			writeErr(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		// On the first authenticated HTTPS request, upgrade the temporary
		// setup cookie to a __Host- HTTPS-only cookie.
		if requestUsesHTTPS(r) {
			if c, cookieErr := r.Cookie(setupSessionCookieName); cookieErr == nil && c.Value != "" {
				// Create the replacement first. Deleting the only valid setup session
				// before entropy or database insertion succeeds can lock the administrator
				// out during the HTTP-to-HTTPS transition.
				if sessionErr := s.createSession(w, r, uid, false); sessionErr != nil {
					writeErr(w, http.StatusInternalServerError, "could not rotate session")
					return
				}
				if _, deleteErr := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenDigest(c.Value)); deleteErr != nil {
					// The new secure session is already valid. Keep serving the request and
					// clear the browser's setup cookie; routine maintenance will remove the
					// orphaned database row later.
					s.audit(r, uid, "auth.setup_session_cleanup_failed", "session", "", map[string]any{})
				}
				clearCookie(w, setupSessionCookieName, false)
			}
		}
		next(w, r.WithContext(withUserID(r.Context(), uid)))
	}
}
