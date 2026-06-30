package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIPInfoTokenSettingsAreWriteOnlyEncryptedAndDynamic(t *testing.T) {
	s, uid := newRegressionServer(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer valid-test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/1.1.1.1" {
			http.Error(w, "unexpected IP", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"1.1.1.1","country_code":"AU"}`))
	}))
	defer provider.Close()

	s.geo = newCountryGeoResolverWithConfig(s.db, s.masterKey, countryGeoResolverConfig{
		Endpoint: provider.URL, Client: provider.Client(), Workers: 1, QueueSize: 8,
		CacheTTL: time.Hour, NegativeTTL: time.Minute, MemoryEntries: 8,
	})
	defer s.geo.close()

	req := httptest.NewRequest(http.MethodPut, "/api/settings/ipinfo-token", strings.NewReader(`{"token":"valid-test-token"}`))
	req = req.WithContext(withUserID(req.Context(), uid))
	rr := httptest.NewRecorder()
	s.handleSaveIPInfoToken(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("save token status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "valid-test-token") {
		t.Fatal("token was returned in the API response")
	}
	var status ipinfoTokenStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.HasToken || !status.Configured || !s.geo.configured() {
		t.Fatalf("unexpected saved status: %+v", status)
	}

	var encrypted string
	if err := s.db.QueryRow(`SELECT value FROM config WHERE key=?`, ipinfoTokenConfigKey).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if encrypted == "valid-test-token" || strings.Contains(encrypted, "valid-test-token") {
		t.Fatal("plaintext IPinfo token was stored")
	}
	plain, err := decryptIPInfoToken(s.masterKey, encrypted)
	if err != nil || plain != "valid-test-token" {
		t.Fatalf("stored token could not be decrypted: token=%q err=%v", plain, err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/ipinfo-token", nil)
	getReq = getReq.WithContext(withUserID(getReq.Context(), uid))
	getRR := httptest.NewRecorder()
	s.handleGetIPInfoToken(getRR, getReq)
	if strings.Contains(getRR.Body.String(), "valid-test-token") {
		t.Fatal("GET endpoint exposed the token")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/settings/ipinfo-token", nil)
	deleteReq = deleteReq.WithContext(withUserID(deleteReq.Context(), uid))
	deleteRR := httptest.NewRecorder()
	s.handleDeleteIPInfoToken(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("delete token status=%d body=%s", deleteRR.Code, deleteRR.Body.String())
	}
	if s.geo.configured() {
		t.Fatal("resolver remained configured after token deletion")
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM config WHERE key=?`, ipinfoTokenConfigKey).Scan(&count)
	if count != 0 {
		t.Fatalf("encrypted token row remained after deletion: %d", count)
	}
}

func TestLegacyIPInfoEnvironmentTokenMigratesOnlyOnce(t *testing.T) {
	db := openDB(t.TempDir() + "/vector.db")
	defer db.Close()
	masterKey := []byte("migration-master-key")
	t.Setenv("IPINFO_TOKEN", "legacy-token")

	token, err := loadOrMigrateIPInfoToken(db, masterKey)
	if err != nil || token != "legacy-token" {
		t.Fatalf("migration token=%q err=%v", token, err)
	}
	var encrypted string
	if err := db.QueryRow(`SELECT value FROM config WHERE key=?`, ipinfoTokenConfigKey).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if encrypted == "legacy-token" {
		t.Fatal("legacy environment token was stored in plaintext")
	}

	if _, err := db.Exec(`DELETE FROM config WHERE key=?`, ipinfoTokenConfigKey); err != nil {
		t.Fatal(err)
	}
	token, err = loadOrMigrateIPInfoToken(db, masterKey)
	if err != nil || token != "" {
		t.Fatalf("deleted token was re-imported from environment: token=%q err=%v", token, err)
	}
}

func TestHelperSocketUsesAccessibleTopLevelRuntimePath(t *testing.T) {
	if helperSocketPath != "/run/vector-helper.sock" {
		t.Fatalf("helper socket path=%q", helperSocketPath)
	}
	for _, path := range []string{"../packaging/systemd/vector-helper.socket", "../packaging/systemd/vector.service"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "/run/vector-helper.sock") {
			t.Fatalf("%s does not use the accessible helper socket path", path)
		}
		if strings.Contains(string(content), "/run/vector/helper.sock") {
			t.Fatalf("%s still contains the inaccessible nested socket path", path)
		}
	}
}
