package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

//go:embed all:web
var embeddedWeb embed.FS

type server struct {
	db               *sql.DB
	masterKey        []byte
	legacySecret     []byte
	publicBaseURL    string
	baseURLMu        sync.RWMutex
	setupSubmitMu    sync.Mutex
	domainMutationMu sync.Mutex
	linkMutationMu   sync.Mutex
	userMutationMu   sync.Mutex
	geo              *countryGeoResolver

	analyticsHealthMu        sync.RWMutex
	analyticsCaptureFailures uint64
	analyticsLastError       string
	analyticsLastErrorAt     time.Time
	analyticsLastSuccessAt   time.Time
	analyticsLastErrorLogAt  time.Time

	analyticsReportCacheMu sync.RWMutex
	analyticsReportCache   map[string]analyticsReportCacheEntry
	configCacheMu          sync.RWMutex
	configCache            map[string]configCacheEntry
}

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildTime    = "unknown"
)

var reservedCodes = map[string]bool{
	"links": true, "analytics": true, "qr": true, "domains": true,
	"settings": true, "admin": true, "api": true, "login": true, "logout": true,
	"static": true, "assets": true, "favicon.ico": true, "setup": true,
	"unlock": true, ".well-known": true, "healthz": true, "robots.txt": true, "": true,
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("Vector %s (%s, %s)\n", buildVersion, buildCommit, buildTime)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "helper" {
		if err := runPrivilegedHelper(); err != nil {
			log.Fatal(err)
		}
		return
	}
	dataDir := getenv("DATA_DIR", "/opt/vector/data")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		log.Fatalf("cannot create data directory %s: %v", dataDir, err)
	}
	db := openDB(dataDir + "/vector.db")
	defer db.Close()
	masterKey, legacySecret, err := loadMasterKey(db, dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := configureTrustedProxySecret(); err != nil {
		log.Fatalf("load trusted reverse-proxy key: %v", err)
	}
	s := &server{db: db, masterKey: masterKey, legacySecret: legacySecret, publicBaseURL: "http://localhost:8080"}
	if err := s.migrateGeoCountrySource(); err != nil {
		log.Fatalf("country-source migration failed: %v", err)
	}
	s.geo = newCountryGeoResolver(db, masterKey)
	defer s.geo.close()
	if err := s.migratePrivacyData(); err != nil {
		log.Fatalf("privacy migration failed: %v", err)
	}
	if err := s.migrateEncryptedSecrets(); err != nil {
		log.Fatalf("encrypted secret migration failed: %v", err)
	}
	domain, err := s.getConfigE("domain")
	if err != nil {
		log.Fatalf("load configured domain: %v", err)
	}
	if domain != "" {
		s.setPublicBaseURL("https://" + domain)
	}

	s.startMaintenance()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/security/csrf", s.handleCSRF)

	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/setup/bootstrap/login", s.handleBootstrapLogin)
	mux.HandleFunc("POST /api/setup/bootstrap/logout", s.handleBootstrapLogout)
	mux.HandleFunc("POST /api/setup", s.requireBootstrap(s.handleSetupSubmit))
	mux.HandleFunc("POST /api/setup/check-domain", s.requireBootstrap(s.handleSetupCheckDomain))
	mux.HandleFunc("POST /api/setup/nginx", s.requireBootstrap(s.requireAuth(s.requireSystemAdmin(s.handleSetupNginx))))

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/auth/update-password", s.requireAuth(s.handleUpdatePassword))

	mux.HandleFunc("GET /api/links/check-alias", s.requireAuth(s.handleCheckAlias))
	mux.HandleFunc("GET /api/links", s.requireAuth(s.handleListLinks))
	mux.HandleFunc("POST /api/links", s.requireAuth(s.handleCreateLink))
	mux.HandleFunc("GET /api/links/{id}", s.requireAuth(s.handleGetLink))
	mux.HandleFunc("PUT /api/links/{id}", s.requireAuth(s.handleUpdateLink))
	mux.HandleFunc("DELETE /api/links/{id}", s.requireAuth(s.handleDeleteLink))
	mux.HandleFunc("GET /api/links/{id}/qrcode.png", s.requireAuth(s.handleLinkQRCode))

	mux.HandleFunc("GET /api/analytics/report", s.requireAuth(s.handleAnalyticsReport))
	mux.HandleFunc("GET /api/stats/overview", s.requireAuth(s.handleStatsOverview))
	mux.HandleFunc("GET /api/stats/timeseries", s.requireAuth(s.handleStatsTimeseries))
	mux.HandleFunc("GET /api/stats/geo", s.requireAuth(s.handleStatsGeo))
	mux.HandleFunc("GET /api/stats/devices", s.requireAuth(s.handleStatsDevices))
	mux.HandleFunc("GET /api/stats/browsers", s.requireAuth(s.handleStatsBrowsers))
	mux.HandleFunc("GET /api/stats/referrers", s.requireAuth(s.handleStatsReferrers))
	mux.HandleFunc("GET /api/stats/top-links", s.requireAuth(s.handleStatsTopLinks))
	mux.HandleFunc("GET /api/stats/hours", s.requireAuth(s.handleStatsHours))
	mux.HandleFunc("GET /api/stats/options", s.requireAuth(s.handleStatsOptions))

	mux.HandleFunc("GET /api/domains", s.requireAuth(s.handleListDomains))
	mux.HandleFunc("POST /api/domains", s.requireAuth(s.handleAddDomain))
	mux.HandleFunc("POST /api/domains/{id}/verify", s.requireAuth(s.handleVerifyDomain))
	mux.HandleFunc("POST /api/domains/{id}/default", s.requireAuth(s.handleSetDefaultDomain))
	mux.HandleFunc("DELETE /api/domains/{id}", s.requireAuth(s.handleDeleteDomain))
	mux.HandleFunc("POST /api/domains/{id}/token", s.requireAuth(s.handleDomainTokenSave))
	mux.HandleFunc("DELETE /api/domains/{id}/token", s.requireAuth(s.handleDomainTokenDelete))
	mux.HandleFunc("GET /api/domains/{id}/blocklist", s.requireAuth(s.handleGetSubdomainBlocklist))
	mux.HandleFunc("GET /api/domains/{id}/members", s.requireAuth(s.handleListDomainMembers))
	mux.HandleFunc("POST /api/domains/{id}/members", s.requireAuth(s.handleAddDomainMember))
	mux.HandleFunc("DELETE /api/domains/{id}/members/{userID}", s.requireAuth(s.handleDeleteDomainMember))

	mux.HandleFunc("GET /api/admin/users", s.requireAuth(s.requireAdmin(s.handleAdminListUsers)))
	mux.HandleFunc("POST /api/admin/users", s.requireAuth(s.requireAdmin(s.handleAdminCreateUser)))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.requireAuth(s.requireAdmin(s.handleAdminDeactivateUser)))
	mux.HandleFunc("POST /api/admin/users/{id}/reactivate", s.requireAuth(s.requireAdmin(s.handleAdminReactivateUser)))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-password", s.requireAuth(s.requireAdmin(s.handleAdminResetPassword)))

	// Privacy, retention, portability and security-audit controls.
	mux.HandleFunc("GET /api/settings/privacy", s.requireAuth(s.handleGetPrivacySettings))
	mux.HandleFunc("PUT /api/settings/privacy", s.requireAuth(s.requireSystemAdmin(s.handleUpdatePrivacySettings)))
	mux.HandleFunc("GET /api/settings/ipinfo-token", s.requireAuth(s.requireSystemAdmin(s.handleGetIPInfoToken)))
	mux.HandleFunc("PUT /api/settings/ipinfo-token", s.requireAuth(s.requireSystemAdmin(s.handleSaveIPInfoToken)))
	mux.HandleFunc("DELETE /api/settings/ipinfo-token", s.requireAuth(s.requireSystemAdmin(s.handleDeleteIPInfoToken)))
	mux.HandleFunc("DELETE /api/settings/analytics", s.requireAuth(s.handleDeleteAnalytics))
	mux.HandleFunc("GET /api/settings/export", s.requireAuth(s.handleDataExport))
	mux.HandleFunc("GET /api/audit", s.requireAuth(s.handleAuditLog))

	webFS, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		log.Fatal(err)
	}
	staticHandler := http.FileServer(http.FS(webFS))
	mux.HandleFunc("/", s.handleAll(webFS, staticHandler))

	chain := s.productionHandler(mux)
	addr := getenv("LISTEN_ADDR", "127.0.0.1:8081")
	srv := &http.Server{
		Addr: addr, Handler: chain,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		log.Printf("Vector listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (s *server) productionHandler(next http.Handler) http.Handler {
	return securityHeaders(
		s.setupGuard(
			s.blockDirectIPAfterSetup(
				s.restrictLinkSubdomainSurface(
					s.csrfProtect(
						logRequests(next),
					),
				),
			),
		),
	)
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "unhealthy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
	})
}

func atoiOr(s string, fallback int64) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func (s *server) setPublicBaseURL(value string) {
	s.baseURLMu.Lock()
	s.publicBaseURL = value
	s.baseURLMu.Unlock()
}

func (s *server) getPublicBaseURL() string {
	s.baseURLMu.RLock()
	value := s.publicBaseURL
	s.baseURLMu.RUnlock()
	return value
}
