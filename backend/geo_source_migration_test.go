package main

import "testing"

func TestGeoSourceMigrationClearsOnlyCountryData(t *testing.T) {
	s, uid := newRegressionServer(t)
	res, err := s.db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,status,click_count,lifetime_click_count)
		VALUES(?,?,?,?, 'active',4,9)`, uid, "geo-migrate", "https://example.org", "primary.example.com")
	if err != nil {
		t.Fatal(err)
	}
	linkID, _ := res.LastInsertId()
	if _, err = s.db.Exec(`INSERT INTO analytics_events(link_id,visitor_hash,referrer,device,browser,operating_system,country_code)
		VALUES(?,?,?,?,?,?,?)`, linkID, "visitor-hash", "https://ref.example", "Mobile", "Safari", "iOS", "SG"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO clicks(link_id,visitor_hash,referrer,device,browser,country_code)
		VALUES(?,?,?,?,?,?)`, linkID, "legacy-visitor", "https://legacy.example", "Desktop", "Chrome", "SG"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO geo_country_cache(ip_hash,country_code,expires_at) VALUES('edge','SG',datetime('now','+1 day'))`); err != nil {
		t.Fatal(err)
	}
	_, _ = s.db.Exec(`DELETE FROM config WHERE key='geo_country_source_version'`)

	if err = s.migrateGeoCountrySource(); err != nil {
		t.Fatal(err)
	}
	var country, visitor, browser, device, osName, referrer string
	if err = s.db.QueryRow(`SELECT country_code,visitor_hash,browser,device,operating_system,referrer FROM analytics_events WHERE link_id=?`, linkID).Scan(&country, &visitor, &browser, &device, &osName, &referrer); err != nil {
		t.Fatal(err)
	}
	if country != "" || visitor != "visitor-hash" || browser != "Safari" || device != "Mobile" || osName != "iOS" || referrer != "https://ref.example" {
		t.Fatalf("unexpected preserved event: country=%q visitor=%q browser=%q device=%q os=%q referrer=%q", country, visitor, browser, device, osName, referrer)
	}
	var visible, lifetime int64
	if err = s.db.QueryRow(`SELECT click_count,lifetime_click_count FROM links WHERE id=?`, linkID).Scan(&visible, &lifetime); err != nil {
		t.Fatal(err)
	}
	if visible != 4 || lifetime != 9 {
		t.Fatalf("counters changed: %d/%d", visible, lifetime)
	}
	var cache int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM geo_country_cache`).Scan(&cache)
	if cache != 0 {
		t.Fatalf("geo cache rows=%d", cache)
	}
	if err = s.migrateGeoCountrySource(); err != nil {
		t.Fatalf("idempotent run: %v", err)
	}
}
