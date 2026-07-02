package main

import (
	"database/sql"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func createTenancyUser(t *testing.T, s *server, email, role, password string) int64 {
	t.Helper()
	hash, err := hashPasswordWithError(password)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO users(email,password_hash,password_changed_at,role,disabled)
		VALUES(?,?,CURRENT_TIMESTAMP,?,0)`, email, hash, role)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func createActiveDomain(t *testing.T, s *server, uid int64, hostname string, isDefault bool) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default,message)
		VALUES(?,?,'active',?,'ready')`, uid, hostname, isDefault)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func requestForUser(method, target, body string, uid int64) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request.WithContext(withUserID(request.Context(), uid))
}

func TestRegularUserLoginRequiresMultiUserMode(t *testing.T) {
	s, _ := newRegressionServer(t)
	password := "regular-user-passphrase"
	createTenancyUser(t, s, "user@example.com", "user", password)

	login := func() int {
		r := httptest.NewRequest(http.MethodPost, "https://primary.example.com/api/auth/login",
			strings.NewReader(`{"email":"user@example.com","password":"regular-user-passphrase"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		return w.Code
	}

	if got := login(); got != http.StatusUnauthorized {
		t.Fatalf("single-user regular login status=%d, want 401", got)
	}
	s.setConfig("deployment_mode", deploymentModeMulti)
	if got := login(); got != http.StatusOK {
		t.Fatalf("multi-user regular login status=%d, want 200", got)
	}
}

func TestRegularSessionRejectedAfterSwitchToSingle(t *testing.T) {
	s, _ := newRegressionServer(t)
	uid := createTenancyUser(t, s, "session-user@example.com", "user", "session-user-passphrase")
	s.setConfig("deployment_mode", deploymentModeMulti)
	token := "session-token-for-tenancy-test"
	now := time.Now().UTC()
	if _, err := s.db.Exec(`INSERT INTO sessions(token_hash,user_id,created_at,last_seen_at,expires_at,user_agent_hash)
		VALUES(?,?,?,?,?,?)`, tokenDigest(token), uid, now, now, now.Add(time.Hour), userAgentDigest("tenancy-test")); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://primary.example.com/api/auth/me", nil)
	r.Header.Set("User-Agent", "tenancy-test")
	if got, err := s.authenticatedUserToken(r, token); err != nil || got != uid {
		t.Fatalf("multi-user session got uid=%d err=%v", got, err)
	}
	s.setConfig("deployment_mode", deploymentModeSingle)
	if _, err := s.authenticatedUserToken(r, token); err == nil {
		t.Fatal("regular session remained valid after switching to single-user mode")
	}
}

func TestSharedDomainLifecycleAndPermissionBoundaries(t *testing.T) {
	s, ownerID := newRegressionServer(t)
	s.setConfig("deployment_mode", deploymentModeMulti)
	memberID := createTenancyUser(t, s, "member@example.com", "user", "member-user-passphrase")
	ownedFallbackID := createActiveDomain(t, s, memberID, "member.example.com", true)
	sharedID := createActiveDomain(t, s, ownerID, "shared.example.com", true)
	ownerToken, err := s.encryptDomainToken("shared.example.com", "owner-only-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE domains SET cloudflare_token_enc=? WHERE id=?`, ownerToken, sharedID); err != nil {
		t.Fatal(err)
	}

	add := requestForUser(http.MethodPost, "/api/domains/1/members", `{"email":"member@example.com"}`, ownerID)
	add.SetPathValue("id", idString(sharedID))
	addRecorder := httptest.NewRecorder()
	s.handleAddDomainMember(addRecorder, add)
	if addRecorder.Code != http.StatusCreated {
		t.Fatalf("share status=%d body=%s", addRecorder.Code, addRecorder.Body.String())
	}

	duplicate := requestForUser(http.MethodPost, "/api/domains/1/members", `{"email":"member@example.com"}`, ownerID)
	duplicate.SetPathValue("id", idString(sharedID))
	duplicateRecorder := httptest.NewRecorder()
	s.handleAddDomainMember(duplicateRecorder, duplicate)
	if duplicateRecorder.Code != http.StatusConflict {
		t.Fatalf("duplicate share status=%d body=%s", duplicateRecorder.Code, duplicateRecorder.Body.String())
	}

	listDomains := requestForUser(http.MethodGet, "/api/domains", "", memberID)
	listDomainsRecorder := httptest.NewRecorder()
	s.handleListDomains(listDomainsRecorder, listDomains)
	if listDomainsRecorder.Code != http.StatusOK {
		t.Fatalf("member domain list status=%d body=%s", listDomainsRecorder.Code, listDomainsRecorder.Body.String())
	}
	listed := listDomainsRecorder.Body.String()
	if !strings.Contains(listed, `"hostname":"shared.example.com"`) ||
		!strings.Contains(listed, `"access_role":"member"`) ||
		!strings.Contains(listed, `"owner_email":"admin@example.com"`) ||
		strings.Contains(listed, `"has_token":true`) {
		t.Fatalf("shared domain metadata leaked or was incomplete: %s", listed)
	}

	saveToken := requestForUser(http.MethodPut, "/api/domains/1/token", `{"token":"member-must-not-replace-owner-token"}`, memberID)
	saveToken.SetPathValue("id", idString(sharedID))
	saveTokenRecorder := httptest.NewRecorder()
	s.handleDomainTokenSave(saveTokenRecorder, saveToken)
	if saveTokenRecorder.Code != http.StatusNotFound {
		t.Fatalf("member token save status=%d body=%s", saveTokenRecorder.Code, saveTokenRecorder.Body.String())
	}

	deleteToken := requestForUser(http.MethodDelete, "/api/domains/1/token", "", memberID)
	deleteToken.SetPathValue("id", idString(sharedID))
	deleteTokenRecorder := httptest.NewRecorder()
	s.handleDomainTokenDelete(deleteTokenRecorder, deleteToken)
	if deleteTokenRecorder.Code != http.StatusNotFound {
		t.Fatalf("member token delete status=%d body=%s", deleteTokenRecorder.Code, deleteTokenRecorder.Body.String())
	}

	blocklist := requestForUser(http.MethodGet, "/api/domains/1/blocklist", "", memberID)
	blocklist.SetPathValue("id", idString(sharedID))
	blocklistRecorder := httptest.NewRecorder()
	s.handleGetSubdomainBlocklist(blocklistRecorder, blocklist)
	if blocklistRecorder.Code != http.StatusNotFound {
		t.Fatalf("member DNS inventory status=%d body=%s", blocklistRecorder.Code, blocklistRecorder.Body.String())
	}

	setDefault := requestForUser(http.MethodPost, "/api/domains/1/default", "", memberID)
	setDefault.SetPathValue("id", idString(sharedID))
	defaultRecorder := httptest.NewRecorder()
	s.handleSetDefaultDomain(defaultRecorder, setDefault)
	if defaultRecorder.Code != http.StatusOK {
		t.Fatalf("set shared default status=%d body=%s", defaultRecorder.Code, defaultRecorder.Body.String())
	}

	create := requestForUser(http.MethodPost, "/api/links", `{"destination_url":"https://destination.example/path","custom_alias":"member-link","domain":"shared.example.com","redirect_type":"slug"}`, memberID)
	createRecorder := httptest.NewRecorder()
	s.handleCreateLink(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("member create link status=%d body=%s", createRecorder.Code, createRecorder.Body.String())
	}

	verify := requestForUser(http.MethodPost, "/api/domains/1/verify", "", memberID)
	verify.SetPathValue("id", idString(sharedID))
	verifyRecorder := httptest.NewRecorder()
	s.handleVerifyDomain(verifyRecorder, verify)
	if verifyRecorder.Code != http.StatusNotFound {
		t.Fatalf("member verify status=%d body=%s", verifyRecorder.Code, verifyRecorder.Body.String())
	}

	members := requestForUser(http.MethodGet, "/api/domains/1/members", "", memberID)
	members.SetPathValue("id", idString(sharedID))
	membersRecorder := httptest.NewRecorder()
	s.handleListDomainMembers(membersRecorder, members)
	if membersRecorder.Code != http.StatusNotFound {
		t.Fatalf("member access-list status=%d body=%s", membersRecorder.Code, membersRecorder.Body.String())
	}

	deleteDomain := requestForUser(http.MethodDelete, "/api/domains/1", "", memberID)
	deleteDomain.SetPathValue("id", idString(sharedID))
	deleteRecorder := httptest.NewRecorder()
	s.handleDeleteDomain(deleteRecorder, deleteDomain)
	if deleteRecorder.Code != http.StatusNotFound {
		t.Fatalf("member domain delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	remove := requestForUser(http.MethodDelete, "/api/domains/1/members/2", "", ownerID)
	remove.SetPathValue("id", idString(sharedID))
	remove.SetPathValue("userID", idString(memberID))
	removeRecorder := httptest.NewRecorder()
	s.handleDeleteDomainMember(removeRecorder, remove)
	if removeRecorder.Code != http.StatusOK {
		t.Fatalf("remove access status=%d body=%s", removeRecorder.Code, removeRecorder.Body.String())
	}

	var linkCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE user_id=? AND domain='shared.example.com'`, memberID).Scan(&linkCount); err != nil || linkCount != 1 {
		t.Fatalf("existing shared-domain links count=%d err=%v", linkCount, err)
	}
	var repairedDefault int64
	if err := s.db.QueryRow(`SELECT domain_id FROM domain_members WHERE user_id=? AND is_default=1`, memberID).Scan(&repairedDefault); err != nil || repairedDefault != ownedFallbackID {
		t.Fatalf("repaired default=%d want=%d err=%v", repairedDefault, ownedFallbackID, err)
	}

	blocked := requestForUser(http.MethodPost, "/api/links", `{"destination_url":"https://destination.example/second","custom_alias":"blocked-link","domain":"shared.example.com","redirect_type":"slug"}`, memberID)
	blockedRecorder := httptest.NewRecorder()
	s.handleCreateLink(blockedRecorder, blocked)
	if blockedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("post-removal create status=%d body=%s", blockedRecorder.Code, blockedRecorder.Body.String())
	}

	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("index")}}
	redirectRequest := httptest.NewRequest(http.MethodGet, "https://shared.example.com/member-link", nil)
	redirectRequest.Host = "shared.example.com"
	redirectRecorder := httptest.NewRecorder()
	s.handleAll(fs.FS(web), http.NotFoundHandler())(redirectRecorder, redirectRequest)
	if redirectRecorder.Code != http.StatusFound || redirectRecorder.Header().Get("Location") != "https://destination.example/path" {
		t.Fatalf("existing link status=%d location=%q body=%s", redirectRecorder.Code, redirectRecorder.Header().Get("Location"), redirectRecorder.Body.String())
	}
}

func TestUserWithoutAccessibleDomainHasNoImplicitGlobalDefault(t *testing.T) {
	s, _ := newRegressionServer(t)
	s.setConfig("deployment_mode", deploymentModeMulti)
	uid := createTenancyUser(t, s, "no-domain@example.com", "user", "no-domain-user-passphrase")

	domain, err := s.defaultDomainForUserE(uid)
	if err != nil {
		t.Fatal(err)
	}
	if domain != "" {
		t.Fatalf("default domain=%q, want empty for a user without domain access", domain)
	}
}

func TestAdminSoftDeactivatePreservesContentAndRedirect(t *testing.T) {
	s, adminID := newRegressionServer(t)
	s.setConfig("deployment_mode", deploymentModeMulti)
	userID := createTenancyUser(t, s, "deactivate@example.com", "user", "deactivate-user-passphrase")
	createActiveDomain(t, s, userID, "deactivate.example.com", true)
	if _, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type,status)
		VALUES(?, 'still-live', 'https://destination.example/live', 'deactivate.example.com', 'slug', 'active')`, userID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := s.db.Exec(`INSERT INTO sessions(token_hash,user_id,created_at,last_seen_at,expires_at,user_agent_hash)
		VALUES('dead-session',?,?,?,?, '')`, userID, now, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	r := requestForUser(http.MethodDelete, "/api/admin/users/2", "", adminID)
	r.SetPathValue("id", idString(userID))
	w := httptest.NewRecorder()
	s.handleAdminDeactivateUser(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("deactivate status=%d body=%s", w.Code, w.Body.String())
	}
	var disabled, links, domains, sessions int
	_ = s.db.QueryRow(`SELECT disabled FROM users WHERE id=?`, userID).Scan(&disabled)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE user_id=?`, userID).Scan(&links)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM domains WHERE user_id=?`, userID).Scan(&domains)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, userID).Scan(&sessions)
	if disabled != 1 || links != 1 || domains != 1 || sessions != 0 {
		t.Fatalf("disabled=%d links=%d domains=%d sessions=%d", disabled, links, domains, sessions)
	}

	redirectRequest := httptest.NewRequest(http.MethodGet, "https://deactivate.example.com/still-live", nil)
	redirectRequest.Host = "deactivate.example.com"
	redirectRecorder := httptest.NewRecorder()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("index")}}
	s.handleAll(fs.FS(web), http.NotFoundHandler())(redirectRecorder, redirectRequest)
	if redirectRecorder.Code != http.StatusFound || redirectRecorder.Header().Get("Location") != "https://destination.example/live" {
		t.Fatalf("soft-disabled redirect status=%d location=%q", redirectRecorder.Code, redirectRecorder.Header().Get("Location"))
	}
}

func TestAdminAPIIsHiddenInSingleModeAndForbiddenToRegularUsers(t *testing.T) {
	s, adminID := newRegressionServer(t)
	userID := createTenancyUser(t, s, "not-admin@example.com", "user", "not-admin-passphrase")
	handler := s.requireAdmin(s.handleAdminListUsers)

	singleRequest := requestForUser(http.MethodGet, "/api/admin/users", "", adminID)
	singleRecorder := httptest.NewRecorder()
	handler(singleRecorder, singleRequest)
	if singleRecorder.Code != http.StatusNotFound {
		t.Fatalf("single mode admin API status=%d", singleRecorder.Code)
	}

	s.setConfig("deployment_mode", deploymentModeMulti)
	regularRequest := requestForUser(http.MethodGet, "/api/admin/users", "", userID)
	regularRecorder := httptest.NewRecorder()
	handler(regularRecorder, regularRequest)
	if regularRecorder.Code != http.StatusForbidden {
		t.Fatalf("regular user admin API status=%d", regularRecorder.Code)
	}

	setupNginx := s.requireSystemAdmin(s.handleSetupNginx)
	setupRequest := requestForUser(http.MethodPost, "/api/setup/nginx", `{"domain":"primary.example.com"}`, userID)
	setupRecorder := httptest.NewRecorder()
	setupNginx(setupRecorder, setupRequest)
	if setupRecorder.Code != http.StatusForbidden {
		t.Fatalf("regular user privileged setup status=%d body=%s", setupRecorder.Code, setupRecorder.Body.String())
	}

	adminRequest := requestForUser(http.MethodGet, "/api/admin/users", "", adminID)
	adminRecorder := httptest.NewRecorder()
	handler(adminRecorder, adminRequest)
	if adminRecorder.Code != http.StatusOK {
		t.Fatalf("multi mode admin API status=%d body=%s", adminRecorder.Code, adminRecorder.Body.String())
	}
}

func TestAuditAndAnalyticsDeletionAreTenantScoped(t *testing.T) {
	s, adminID := newRegressionServer(t)
	s.setConfig("deployment_mode", deploymentModeMulti)
	userID := createTenancyUser(t, s, "audit-user@example.com", "user", "audit-user-passphrase")

	s.audit(nil, adminID, "admin.private", "system", "admin", nil)
	s.audit(nil, userID, "user.own", "user", idString(userID), nil)
	s.audit(nil, 0, "system.global", "system", "global", nil)

	regularAudit := requestForUser(http.MethodGet, "/api/audit", "", userID)
	regularAuditRecorder := httptest.NewRecorder()
	s.handleAuditLog(regularAuditRecorder, regularAudit)
	if regularAuditRecorder.Code != http.StatusOK {
		t.Fatalf("regular audit status=%d body=%s", regularAuditRecorder.Code, regularAuditRecorder.Body.String())
	}
	regularBody := regularAuditRecorder.Body.String()
	if !strings.Contains(regularBody, "user.own") || strings.Contains(regularBody, "admin.private") || strings.Contains(regularBody, "system.global") {
		t.Fatalf("regular audit leaked cross-tenant entries: %s", regularBody)
	}

	adminAudit := requestForUser(http.MethodGet, "/api/audit", "", adminID)
	adminAuditRecorder := httptest.NewRecorder()
	s.handleAuditLog(adminAuditRecorder, adminAudit)
	if adminAuditRecorder.Code != http.StatusOK {
		t.Fatalf("admin audit status=%d body=%s", adminAuditRecorder.Code, adminAuditRecorder.Body.String())
	}
	adminBody := adminAuditRecorder.Body.String()
	if !strings.Contains(adminBody, "user.own") || !strings.Contains(adminBody, "admin.private") || !strings.Contains(adminBody, "system.global") {
		t.Fatalf("admin audit omitted deployment entries: %s", adminBody)
	}

	if _, err := s.db.Exec(`INSERT INTO geo_country_cache(ip_hash,country_code,expires_at) VALUES('tenant-test','IN',datetime('now','+1 day'))`); err != nil {
		t.Fatal(err)
	}
	regularDelete := requestForUser(http.MethodDelete, "/api/settings/analytics", "", userID)
	regularDeleteRecorder := httptest.NewRecorder()
	s.handleDeleteAnalytics(regularDeleteRecorder, regularDelete)
	if regularDeleteRecorder.Code != http.StatusOK {
		t.Fatalf("regular analytics delete status=%d body=%s", regularDeleteRecorder.Code, regularDeleteRecorder.Body.String())
	}
	var cacheRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM geo_country_cache WHERE ip_hash='tenant-test'`).Scan(&cacheRows); err != nil || cacheRows != 1 {
		t.Fatalf("regular user modified deployment geo cache rows=%d err=%v", cacheRows, err)
	}
}

func TestOnlyOneActiveAdministratorButManyUsersAreAllowed(t *testing.T) {
	s, _ := newRegressionServer(t)
	createTenancyUser(t, s, "first-user@example.com", "user", "first-user-passphrase")
	createTenancyUser(t, s, "second-user@example.com", "user", "second-user-passphrase")
	_, err := s.db.Exec(`INSERT INTO users(email,password_hash,role,disabled) VALUES('second-admin@example.com','x','admin',0)`)
	if err == nil {
		t.Fatal("second active administrator was accepted")
	}
	var userCount int
	if scanErr := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='user' AND disabled=0`).Scan(&userCount); scanErr != nil || userCount != 2 {
		t.Fatalf("active user count=%d err=%v", userCount, scanErr)
	}
}

func TestOwnerMembershipBackfillAndDefaultRepair(t *testing.T) {
	db := openDB(t.TempDir() + "/vector.db")
	t.Cleanup(func() { _ = db.Close() })
	res, err := db.Exec(`INSERT INTO users(email,password_hash,role,disabled) VALUES('owner@example.com','x','admin',0)`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	domainRes, err := db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(?, 'owner.example.com', 'active', 1)`, uid)
	if err != nil {
		t.Fatal(err)
	}
	domainID, _ := domainRes.LastInsertId()
	var role string
	var isDefault bool
	if err := db.QueryRow(`SELECT access_role,is_default FROM domain_members WHERE domain_id=? AND user_id=?`, domainID, uid).Scan(&role, &isDefault); err != nil {
		t.Fatal(err)
	}
	if role != "owner" || !isDefault {
		t.Fatalf("membership role=%q default=%v", role, isDefault)
	}
}

func TestAdminRouteIsReserved(t *testing.T) {
	if !reservedCodes["admin"] {
		t.Fatal("/admin is not reserved from user-created short codes")
	}
}

var _ = sql.ErrNoRows
