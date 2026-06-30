package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnalyticsV2CapturesBrowserDeviceAndUniqueVisitorsTransactionally(t *testing.T) {
	s, uid := newRegressionServer(t)
	s.setConfig("analytics_enabled", "true")
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status) VALUES(?,?,?,?, 'active')`, uid, "audience", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()

	click := func(ip, ua, mobile string) {
		r := httptest.NewRequest(http.MethodGet, "https://primary.example.com/audience", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", ip)
		r.Header.Set("User-Agent", ua)
		if mobile != "" {
			r.Header.Set("Sec-CH-UA-Mobile", mobile)
		}
		markTrustedProxy(r)
		w := httptest.NewRecorder()
		s.logClickAndRedirect(w, r, linkID, "https://example.org")
		if w.Code != http.StatusFound {
			t.Fatalf("redirect status=%d body=%s", w.Code, w.Body.String())
		}
	}

	chrome := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/149.0.0.0 Safari/537.36"
	safari := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1"
	click("8.8.8.8", chrome, "?0")
	click("8.8.8.8", chrome, "?0")
	click("1.1.1.1", safari, "?1")

	var events, unique int64
	if err := s.db.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT NULLIF(visitor_hash,'')) FROM analytics_events WHERE link_id=?`, linkID).Scan(&events, &unique); err != nil {
		t.Fatal(err)
	}
	if events != 3 || unique != 2 {
		t.Fatalf("events/unique=%d/%d, want 3/2", events, unique)
	}
	var chromeCount, safariCount, desktopCount, mobileCount int64
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM analytics_events WHERE link_id=? AND browser='Chrome'`, linkID).Scan(&chromeCount)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM analytics_events WHERE link_id=? AND browser='Safari'`, linkID).Scan(&safariCount)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM analytics_events WHERE link_id=? AND device='Desktop'`, linkID).Scan(&desktopCount)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM analytics_events WHERE link_id=? AND device='Mobile'`, linkID).Scan(&mobileCount)
	if chromeCount != 2 || safariCount != 1 || desktopCount != 2 || mobileCount != 1 {
		t.Fatalf("classification chrome=%d safari=%d desktop=%d mobile=%d", chromeCount, safariCount, desktopCount, mobileCount)
	}
	var rollup int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(click_count),0) FROM click_rollups WHERE link_id=?`, linkID).Scan(&rollup); err != nil {
		t.Fatal(err)
	}
	if rollup != 3 {
		t.Fatalf("rollup=%d, want 3", rollup)
	}

	filter := analyticsFilter{Range: "30d", Days: 30, Start: time.Now().UTC().AddDate(0, 0, -29), End: time.Now().UTC().Add(time.Second)}
	report, err := s.loadAnalyticsReport(context.Background(), uid, filter)
	if err != nil {
		t.Fatal(err)
	}
	if report.Overview.TotalClicks != 3 || report.Overview.DetailedClicks != 3 || report.Overview.UniqueVisitors != 2 {
		t.Fatalf("overview=%+v", report.Overview)
	}
	if len(report.Browsers) < 2 || len(report.Devices) < 2 {
		t.Fatalf("missing breakdowns browsers=%v devices=%v", report.Browsers, report.Devices)
	}
	if report.SchemaVersion != 2 {
		t.Fatalf("schema=%d", report.SchemaVersion)
	}
}

func TestAnalyticsReportWorksWithSingleSQLiteConnection(t *testing.T) {
	s, uid := newRegressionServer(t)
	s.db.SetMaxOpenConns(1)
	s.setConfig("analytics_enabled", "true")
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status) VALUES(?,?,?,?, 'active')`, uid, "single", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	now := time.Now().UTC()
	_, _ = s.db.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count) VALUES(?,?,1)`, linkID, now.Truncate(time.Hour))
	_, _ = s.db.Exec(`INSERT INTO analytics_events(link_id,occurred_at,visitor_hash,device,browser,operating_system) VALUES(?,?,?,?,?,?)`, linkID, now, "visitor", "Desktop", "Chrome", "Linux")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	report, err := s.loadAnalyticsReport(ctx, uid, analyticsFilter{Range: "7d", Days: 7, Start: now.AddDate(0, 0, -6), End: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Overview.TotalClicks != 1 || report.Overview.UniqueVisitors != 1 {
		t.Fatalf("unexpected report: %+v", report.Overview)
	}
}
