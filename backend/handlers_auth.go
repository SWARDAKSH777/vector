package main

import (
	"database/sql"
	"errors"
	"net/http"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	setupComplete, configErr := s.getConfigE("setup_complete")
	if configErr != nil {
		writeErr(w, http.StatusServiceUnavailable, "authentication configuration is temporarily unavailable")
		return
	}
	if setupComplete == "true" && !requestUsesHTTPS(r) && !isLoopbackRemote(r.RemoteAddr) {
		writeErr(w, http.StatusUpgradeRequired, "administrator login requires HTTPS")
		return
	}

	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil || len(req.Password) > maximumPasswordBytes {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	ip := requestClientIP(r)
	accountKey := stableValueHash(s.masterKey, email)
	if !loginIPLimit.allow(ip) || !loginAcctLimit.allow(accountKey) {
		s.audit(r, 0, "auth.login.rate_limited", "user", "", map[string]any{"email_hash": accountKey})
		writeErr(w, http.StatusTooManyRequests, "too many login attempts; try again later")
		return
	}

	var id int64
	var hash, role string
	var disabled int
	queryErr := s.db.QueryRow(
		`SELECT id, password_hash, role, disabled FROM users WHERE email = ?`, email,
	).Scan(&id, &hash, &role, &disabled)

	// Use a pre-computed dummy hash when the email is not found so that
	// response time is indistinguishable from a failed password comparison.
	verifyHash := hash
	if queryErr != nil {
		verifyHash = loginDummyHash()
	}
	valid := verifyPassword(req.Password, verifyHash)
	if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
		s.audit(r, 0, "auth.login.backend_error", "user", "", map[string]any{"email_hash": accountKey})
		writeErr(w, http.StatusInternalServerError, "could not verify credentials")
		return
	}
	if errors.Is(queryErr, sql.ErrNoRows) || !valid || role != "admin" || disabled != 0 {
		s.audit(r, 0, "auth.login.failed", "user", "", map[string]any{"email_hash": accountKey})
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	// Transparently upgrade stored hash to argon2id on successful login.
	if passwordHashNeedsUpgrade(hash) {
		if upgraded, hashErr := hashPasswordWithError(req.Password); hashErr == nil {
			if _, updateErr := s.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, upgraded, id); updateErr != nil {
				s.audit(r, id, "auth.password_hash_upgrade_failed", "user", idString(id), nil)
			}
		}
	}
	if err := s.createSession(w, r, id, false); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create session")
		return
	}
	s.audit(r, id, "auth.login.succeeded", "user", idString(id), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	uid, _ := s.authenticatedUser(r)
	s.deleteRequestSession(r)
	s.clearSessionCookies(w, r)
	if uid > 0 {
		s.audit(r, uid, "auth.logout", "user", idString(uid), nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	uid := userIDFromCtx(r.Context())
	var email string
	err := s.db.QueryRow(`SELECT email FROM users WHERE id = ?`, uid).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": uid, "email": email, "role": "admin"})
}

type updatePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *server) handleUpdatePassword(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	var req updatePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateAdminPassword(req.NewPassword); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.CurrentPassword == req.NewPassword {
		writeErr(w, http.StatusBadRequest, "new password must be different")
		return
	}

	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&hash); err != nil {
		if err == sql.ErrNoRows {
			writeErr(w, http.StatusNotFound, "user not found")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not load user")
		}
		return
	}
	if !verifyPassword(req.CurrentPassword, hash) {
		s.audit(r, uid, "auth.password_change.failed", "user", idString(uid), nil)
		writeErr(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newHash, err := hashPasswordWithError(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "secure password hashing is temporarily unavailable")
		return
	}

	// Change password and revoke all existing sessions atomically, then issue
	// a fresh session so the user stays logged in.
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	if _, err = tx.Exec(
		`UPDATE users SET password_hash=?, password_changed_at=CURRENT_TIMESTAMP WHERE id=?`,
		newHash, uid,
	); err == nil {
		_, err = tx.Exec(`DELETE FROM sessions WHERE user_id=?`, uid)
	}
	if err != nil {
		_ = tx.Rollback()
		writeErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	if err = tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	if err := s.createSession(w, r, uid, false); err != nil {
		// All prior sessions were revoked in the committed transaction. Remove
		// the stale browser cookie as well so the next request does not appear
		// authenticated with a token that can never succeed.
		s.clearSessionCookies(w, r)
		writeErr(w, http.StatusInternalServerError, "password changed; please log in again")
		return
	}
	s.audit(r, uid, "auth.password_changed", "user", idString(uid), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
