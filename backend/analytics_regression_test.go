package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeleteAnalyticsResetsVisibleCountersButPreservesLifetimeLimits(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,click_count,lifetime_click_count,max_clicks)
		VALUES(?,?,?,?, 'active',7,11,20)`, uid, "stats", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	for i := 0; i < 3; i++ {
		if _, err := s.db.Exec(`INSERT INTO analytics_events(link_id,visitor_hash,country_code) VALUES(?,?,?)`, linkID, "visitor", "IN"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count) VALUES(?,CURRENT_TIMESTAMP,7)`, linkID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO geo_country_cache(ip_hash,country_code,expires_at) VALUES('test','IN',datetime('now','+1 day'))`); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/api/settings/analytics", nil)
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleDeleteAnalytics(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("delete analytics status=%d body=%s", w.Code, w.Body.String())
	}

	var visible, lifetime int64
	if err := s.db.QueryRow(`SELECT click_count,lifetime_click_count FROM links WHERE id=?`, linkID).Scan(&visible, &lifetime); err != nil {
		t.Fatal(err)
	}
	if visible != 0 || lifetime != 11 {
		t.Fatalf("counter state visible=%d lifetime=%d, want 0/11", visible, lifetime)
	}
	var events int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM analytics_events WHERE link_id=?`, linkID).Scan(&events)
	if events != 0 {
		t.Fatalf("click events remaining=%d", events)
	}
	var rollups int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM click_rollups WHERE link_id=?`, linkID).Scan(&rollups)
	if rollups != 0 {
		t.Fatalf("aggregate rollups remaining=%d", rollups)
	}
	var cacheRows int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM geo_country_cache`).Scan(&cacheRows)
	if cacheRows != 0 {
		t.Fatalf("geographic cache rows remaining=%d", cacheRows)
	}
}

func TestClickLimitUsesLifetimeCounterAfterAnalyticsClear(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,click_count,lifetime_click_count,max_clicks)
		VALUES(?,?,?,?, 'active',0,5,5)`, uid, "limited", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	r := httptest.NewRequest(http.MethodGet, "https://primary.example.com/limited", nil)
	w := httptest.NewRecorder()
	s.enforceAndRedirect(w, r, linkID, "https://example.org", "active", sql.NullString{}, sql.NullTime{}, sql.NullInt64{Int64: 5, Valid: true}, "limited")
	if w.Code != http.StatusGone {
		t.Fatalf("limit response=%d, want 410", w.Code)
	}
	var status string
	_ = s.db.QueryRow(`SELECT status FROM links WHERE id=?`, linkID).Scan(&status)
	if status != "paused" {
		t.Fatalf("status=%q, want paused", status)
	}
}

func TestAnalyticsClientIPUsesOnlyNginxNormalizedAddress(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://example.com/x", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "8.8.4.4")
	r.Header.Set("CF-Connecting-IP", "1.1.1.1")
	markTrustedProxy(r)
	if got := analyticsClientIP(r); got != "8.8.4.4" {
		t.Fatalf("analytics client IP=%q, want normalized Nginx value", got)
	}
}

func TestTrustedCloudflareCountryRequiresAuthenticatedProxyMarker(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://example.com/x", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set(vectorCloudflareTrustedHeader, "1")
	r.Header.Set(vectorCountryHeader, "sg")
	if _, ok := trustedCloudflareCountry(r); ok {
		t.Fatal("country trusted without authenticated proxy")
	}
	markTrustedProxy(r)
	if got, ok := trustedCloudflareCountry(r); !ok || got != "SG" {
		t.Fatalf("trusted country=(%q,%v), want SG,true", got, ok)
	}
	r.Header.Set(vectorCountryHeader, "T1")
	if _, ok := trustedCloudflareCountry(r); ok {
		t.Fatal("Tor special code must not be stored as a country")
	}
}

func TestCloudflareCountryCapturedImmediatelyWithoutIPinfo(t *testing.T) {
	s, _ := newRegressionServer(t)
	s.setConfig("analytics_enabled", "true")
	r := httptest.NewRequest(http.MethodGet, "https://example.com/x", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.50")
	r.Header.Set(vectorCloudflareTrustedHeader, "1")
	r.Header.Set(vectorCountryHeader, "IN")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	markTrustedProxy(r)
	capture := s.prepareAnalyticsCapture(r)
	if !capture.CountryKnown || capture.Country != "IN" {
		t.Fatalf("capture country=(%q,%v), want IN,true", capture.Country, capture.CountryKnown)
	}
}

func TestRedirectCountryLookupIsNonBlockingAndStoresNoRawIP(t *testing.T) {
	s, uid := newRegressionServer(t)
	s.setConfig("analytics_enabled", "true")
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status) VALUES(?,?,?,?, 'active')`, uid, "async-geo", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"8.8.8.8","country_code":"US"}`))
	}))
	defer api.Close()
	s.geo = newCountryGeoResolverWithConfig(s.db, s.masterKey, countryGeoResolverConfig{
		Token: "test-token", Endpoint: api.URL, Client: api.Client(), Workers: 1, QueueSize: 8,
		CacheTTL: time.Hour, NegativeTTL: time.Minute,
	})
	defer s.geo.close()

	r := httptest.NewRequest(http.MethodGet, "https://primary.example.com/async-geo", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	markTrustedProxy(r)
	r.Header.Set("User-Agent", "Mozilla/5.0")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.logClickAndRedirect(w, r, linkID, "https://example.org")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("redirect waited for the country provider")
	}
	if w.Code != http.StatusFound {
		t.Fatalf("redirect status=%d", w.Code)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background country lookup did not start")
	}
	var clickID int64
	var country string
	if err := s.db.QueryRow(`SELECT id,country_code FROM analytics_events WHERE link_id=?`, linkID).Scan(&clickID, &country); err != nil {
		t.Fatal(err)
	}
	if country != "" {
		t.Fatalf("unexpected immediate country=%q", country)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := s.db.QueryRow(`SELECT country_code FROM analytics_events WHERE id=?`, clickID).Scan(&country); err != nil {
			t.Fatal(err)
		}
		if country == "US" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for redirect country enrichment")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIPinfoLiteCountryLookupIsAsyncCachedAndDoesNotStoreRawIP(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status) VALUES(?,?,?,?, 'active')`, uid, "geo", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	clickRes, err := s.db.Exec(`INSERT INTO analytics_events(link_id,visitor_hash,country_code) VALUES(?,?,?)`, linkID, "visitor", "")
	if err != nil {
		t.Fatal(err)
	}
	clickID, _ := clickRes.LastInsertId()

	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization header=%q", got)
		}
		if r.URL.Path != "/8.8.8.8" {
			t.Errorf("lookup path=%q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"country_code":"IN"}`))
	}))
	defer api.Close()

	resolver := newCountryGeoResolverWithConfig(s.db, s.masterKey, countryGeoResolverConfig{
		Token: "test-token", Endpoint: api.URL, Client: api.Client(), Workers: 1, QueueSize: 8,
		CacheTTL: time.Hour, NegativeTTL: time.Minute,
	})
	defer resolver.close()
	resolver.enqueue(clickID, "8.8.8.8")

	deadline := time.Now().Add(2 * time.Second)
	for {
		var country string
		if err := s.db.QueryRow(`SELECT country_code FROM analytics_events WHERE id=?`, clickID).Scan(&country); err != nil {
			t.Fatal(err)
		}
		if country == "IN" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for country update")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if country, ok := resolver.cachedCountryForIP("8.8.8.8"); !ok || country != "IN" {
		t.Fatalf("cached country=%q ok=%v", country, ok)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("API calls=%d, want 1", got)
	}
}

func TestIPinfoLiteRejectsMismatchedResponseIP(t *testing.T) {
	s, _ := newRegressionServer(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"1.1.1.1","country_code":"IN"}`))
	}))
	defer api.Close()
	resolver := newCountryGeoResolverWithConfig(s.db, s.masterKey, countryGeoResolverConfig{
		Token: "test-token", Endpoint: api.URL, Client: api.Client(), Workers: 1, QueueSize: 8,
		CacheTTL: time.Hour, NegativeTTL: time.Minute,
	})
	defer resolver.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := resolver.doLookup(ctx, "8.8.8.8"); err == nil {
		t.Fatal("mismatched provider response IP was accepted")
	}
}

func TestCountryResolverResetCancelsInFlightLookup(t *testing.T) {
	s, _ := newRegressionServer(t)
	started := make(chan struct{})
	canceled := make(chan struct{})
	var onceStart sync.Once
	var onceCancel sync.Once
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		onceStart.Do(func() { close(started) })
		<-r.Context().Done()
		onceCancel.Do(func() { close(canceled) })
	}))
	defer api.Close()

	resolver := newCountryGeoResolverWithConfig(s.db, s.masterKey, countryGeoResolverConfig{
		Token: "test-token", Endpoint: api.URL, Client: api.Client(), Workers: 1, QueueSize: 8,
		CacheTTL: time.Hour, NegativeTTL: time.Minute,
	})
	defer resolver.close()
	resolver.enqueue(123, "8.8.8.8")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup did not start")
	}
	resolver.reset()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("reset did not cancel in-flight lookup")
	}
	resolver.mu.Lock()
	pending, memory := len(resolver.pending), len(resolver.memory)
	resolver.mu.Unlock()
	if pending != 0 || memory != 0 || resolver.queueDepth() != 0 {
		t.Fatalf("resolver state remained after reset: pending=%d memory=%d queue=%d", pending, memory, resolver.queueDepth())
	}
}

func TestCountryLookupRejectsPrivateAndDocumentationAddresses(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "203.0.113.8", "2001:db8::1"} {
		if got, ok := publicGeoIP(value); ok || got != "" {
			t.Fatalf("address %s accepted as public: %q", value, got)
		}
	}
	if got, ok := publicGeoIP("8.8.8.8"); !ok || got != "8.8.8.8" {
		t.Fatalf("public address rejected: %q %v", got, ok)
	}
}

func TestGPCClickKeepsAccurateAggregateWithoutDetailedEvent(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status) VALUES(?,?,?,?, 'active')`, uid, "private", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	r := httptest.NewRequest(http.MethodGet, "https://primary.example.com/private", nil)
	r.Header.Set("Sec-GPC", "1")
	w := httptest.NewRecorder()
	s.logClickAndRedirect(w, r, linkID, "https://example.org")
	if w.Code != http.StatusFound {
		t.Fatalf("redirect status=%d", w.Code)
	}
	var visible, lifetime, rollup, events int64
	_ = s.db.QueryRow(`SELECT click_count,lifetime_click_count FROM links WHERE id=?`, linkID).Scan(&visible, &lifetime)
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(click_count),0) FROM click_rollups WHERE link_id=?`, linkID).Scan(&rollup)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM analytics_events WHERE link_id=?`, linkID).Scan(&events)
	if visible != 1 || lifetime != 1 || rollup != 1 || events != 0 {
		t.Fatalf("visible=%d lifetime=%d rollup=%d events=%d", visible, lifetime, rollup, events)
	}
}

func TestPreviewAndHeadRequestsDoNotInflateClicks(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status) VALUES(?,?,?,?, 'active')`, uid, "preview", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodHead, "https://primary.example.com/preview", nil),
		httptest.NewRequest(http.MethodGet, "https://primary.example.com/preview", nil),
	} {
		if req.Method == http.MethodGet {
			req.Header.Set("User-Agent", "Slackbot-LinkExpanding 1.0")
		}
		w := httptest.NewRecorder()
		s.logClickAndRedirect(w, req, linkID, "https://example.org")
		if w.Code != http.StatusFound {
			t.Fatalf("redirect status=%d", w.Code)
		}
	}
	var visible, lifetime int64
	_ = s.db.QueryRow(`SELECT click_count,lifetime_click_count FROM links WHERE id=?`, linkID).Scan(&visible, &lifetime)
	if visible != 0 || lifetime != 0 {
		t.Fatalf("preview requests inflated counters visible=%d lifetime=%d", visible, lifetime)
	}
}

func TestOverviewUsesSameVisibleTotalAsLinksAndRollupsForRange(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,click_count,lifetime_click_count)
		VALUES(?,?,?,?, 'active',9,12)`, uid, "accurate", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	bucket := time.Now().UTC().Truncate(time.Hour)
	if _, err := s.db.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count) VALUES(?,?,9)`, linkID, bucket); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?range=30d", nil)
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleStatsOverview(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		TotalClicks   int64 `json:"total_clicks"`
		AllTimeClicks int64 `json:"all_time_clicks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TotalClicks != 9 || payload.AllTimeClicks != 9 {
		t.Fatalf("overview range/all=%d/%d, want 9/9", payload.TotalClicks, payload.AllTimeClicks)
	}
}

func TestDetailedRetentionDoesNotDeleteAnonymousRollups(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,click_count,lifetime_click_count)
		VALUES(?,?,?,?, 'active',3,3)`, uid, "retention", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	old := time.Now().UTC().AddDate(0, 0, -120).Truncate(time.Hour)
	if _, err := s.db.Exec(`INSERT INTO analytics_events(link_id,occurred_at,visitor_hash) VALUES(?,?,?)`, linkID, old, "old-visitor"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count) VALUES(?,?,3)`, linkID, old); err != nil {
		t.Fatal(err)
	}
	s.setConfig("analytics_retention_days", "90")
	s.runMaintenanceOnce(time.Now().UTC())
	var events, rollups int64
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM analytics_events WHERE link_id=?`, linkID).Scan(&events)
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(click_count),0) FROM click_rollups WHERE link_id=?`, linkID).Scan(&rollups)
	if events != 0 || rollups != 3 {
		t.Fatalf("retention events=%d rollups=%d, want 0/3", events, rollups)
	}
}

func TestTopLinksReturnsUniqueVisitorsInRange(t *testing.T) {
	s, uid := newRegressionServer(t)
	now := time.Now().UTC().Truncate(time.Hour)

	firstResult, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,click_count,lifetime_click_count)
		VALUES(?,?,?,?, 'active',7,7)`, uid, "first", "https://example.org/first", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	firstID, _ := firstResult.LastInsertId()
	secondResult, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,click_count,lifetime_click_count)
		VALUES(?,?,?,?, 'active',3,3)`, uid, "second", "https://example.org/second", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	secondID, _ := secondResult.LastInsertId()

	for _, row := range []struct {
		linkID  int64
		visitor string
	}{
		{firstID, "visitor-a"},
		{firstID, "visitor-a"},
		{firstID, "visitor-b"},
		{firstID, ""},
		{secondID, "visitor-c"},
		{secondID, "visitor-c"},
	} {
		if _, err := s.db.Exec(`INSERT INTO analytics_events(link_id,occurred_at,visitor_hash) VALUES(?,?,?)`, row.linkID, now, row.visitor); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO analytics_events(link_id,occurred_at,visitor_hash) VALUES(?,?,?)`, firstID, now.AddDate(0, 0, -60), "old-visitor"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count) VALUES(?,?,?)`, firstID, now, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count) VALUES(?,?,?)`, secondID, now, 3); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/stats/top-links?range=30d", nil)
	r = r.WithContext(withUserID(r.Context(), uid))
	w := httptest.NewRecorder()
	s.handleStatsTopLinks(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("top-links status=%d body=%s", w.Code, w.Body.String())
	}
	var payload []struct {
		ID      int64 `json:"id"`
		Clicks  int64 `json:"clicks"`
		Unique  int64 `json:"unique"`
		AllTime int64 `json:"all_time_clicks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 {
		t.Fatalf("top-links count=%d, want 2: %s", len(payload), w.Body.String())
	}
	if payload[0].ID != firstID || payload[0].Clicks != 7 || payload[0].Unique != 2 || payload[0].AllTime != 7 {
		t.Fatalf("first top-link=%+v, want id=%d clicks=7 unique=2 all-time=7", payload[0], firstID)
	}
	if payload[1].ID != secondID || payload[1].Clicks != 3 || payload[1].Unique != 1 || payload[1].AllTime != 3 {
		t.Fatalf("second top-link=%+v, want id=%d clicks=3 unique=1 all-time=3", payload[1], secondID)
	}
}
