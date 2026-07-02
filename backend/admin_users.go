package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"urlshortener/sqlite3local"
)

type AdminUser struct {
	ID            int64     `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	Disabled      bool      `json:"disabled"`
	CreatedAt     time.Time `json:"created_at"`
	LinkCount     int64     `json:"link_count"`
	OwnedDomains  int64     `json:"owned_domains"`
	SharedDomains int64     `json:"shared_domains"`
}

func (s *server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT u.id,u.email,u.role,u.disabled,u.created_at,
		(SELECT COUNT(*) FROM links l WHERE l.user_id=u.id),
		(SELECT COUNT(*) FROM domains d WHERE d.user_id=u.id),
		(SELECT COUNT(*) FROM domain_members dm WHERE dm.user_id=u.id AND dm.access_role='member')
		FROM users u ORDER BY CASE u.role WHEN 'admin' THEN 0 ELSE 1 END,u.created_at,u.id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list users")
		return
	}
	defer rows.Close()
	out := []AdminUser{}
	for rows.Next() {
		var item AdminUser
		if err := rows.Scan(&item.ID, &item.Email, &item.Role, &item.Disabled, &item.CreatedAt, &item.LinkCount, &item.OwnedDomains, &item.SharedDomains); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read user data")
			return
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list users")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	s.userMutationMu.Lock()
	defer s.userMutationMu.Unlock()
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateAdminPassword(req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := hashPasswordWithError(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "secure password hashing is temporarily unavailable")
		return
	}
	res, err := s.db.Exec(`INSERT INTO users(email,password_hash,password_changed_at,role,disabled)
		VALUES(?,?,CURRENT_TIMESTAMP,'user',0)`, email, hash)
	if err != nil {
		if sqlite3local.IsConstraint(err) || strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeErr(w, http.StatusConflict, "a user with that email already exists")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not create user")
		}
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "user was created but could not be reloaded")
		return
	}
	s.audit(r, userIDFromCtx(r.Context()), "admin.user_created", "user", idString(id), map[string]any{"email_hash": stableValueHash(s.masterKey, email)})
	writeJSON(w, http.StatusCreated, AdminUser{ID: id, Email: email, Role: "user", CreatedAt: time.Now().UTC()})
}

func (s *server) regularUserForMutation(id int64) error {
	var role string
	err := s.db.QueryRow(`SELECT role FROM users WHERE id=?`, id).Scan(&role)
	if err != nil {
		return err
	}
	if role != "user" {
		return errors.New("administrator account cannot be changed here")
	}
	return nil
}

func (s *server) handleAdminDeactivateUser(w http.ResponseWriter, r *http.Request) {
	s.userMutationMu.Lock()
	defer s.userMutationMu.Unlock()
	id := atoiOr(r.PathValue("id"), -1)
	if err := s.regularUserForMutation(id); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	tx, err := s.db.Begin()
	if err == nil {
		_, err = tx.Exec(`UPDATE users SET disabled=1 WHERE id=? AND role='user'`, id)
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not deactivate user")
		return
	}
	s.audit(r, userIDFromCtx(r.Context()), "admin.user_deactivated", "user", idString(id), map[string]any{"content_preserved": true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content_preserved": true})
}

func (s *server) handleAdminReactivateUser(w http.ResponseWriter, r *http.Request) {
	s.userMutationMu.Lock()
	defer s.userMutationMu.Unlock()
	id := atoiOr(r.PathValue("id"), -1)
	if err := s.regularUserForMutation(id); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if _, err := s.db.Exec(`UPDATE users SET disabled=0 WHERE id=? AND role='user'`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not reactivate user")
		return
	}
	s.audit(r, userIDFromCtx(r.Context()), "admin.user_reactivated", "user", idString(id), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	s.userMutationMu.Lock()
	defer s.userMutationMu.Unlock()
	id := atoiOr(r.PathValue("id"), -1)
	if err := s.regularUserForMutation(id); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateAdminPassword(req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := hashPasswordWithError(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "secure password hashing is temporarily unavailable")
		return
	}
	tx, err := s.db.Begin()
	if err == nil {
		_, err = tx.Exec(`UPDATE users SET password_hash=?,password_changed_at=CURRENT_TIMESTAMP WHERE id=? AND role='user'`, hash, id)
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not reset password")
		return
	}
	s.audit(r, userIDFromCtx(r.Context()), "admin.user_password_reset", "user", idString(id), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
