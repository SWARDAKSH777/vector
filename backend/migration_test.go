package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestV5StyleDatabaseMigratesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	legacy := `
CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT,email TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE domains (id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,hostname TEXT UNIQUE NOT NULL,status TEXT NOT NULL DEFAULT 'pending',message TEXT,cloudflare_zone_id TEXT,proxied INTEGER DEFAULT 0,created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE links (id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,short_code TEXT UNIQUE NOT NULL,destination_url TEXT NOT NULL,domain TEXT NOT NULL DEFAULT '',tag TEXT DEFAULT '',password_hash TEXT,expires_at DATETIME,utm_source TEXT,utm_medium TEXT,utm_campaign TEXT,status TEXT NOT NULL DEFAULT 'active',click_count INTEGER NOT NULL DEFAULT 0,created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE clicks (id INTEGER PRIMARY KEY AUTOINCREMENT,link_id INTEGER NOT NULL,clicked_at DATETIME DEFAULT CURRENT_TIMESTAMP,referrer TEXT,user_agent TEXT,device TEXT,browser TEXT,ip TEXT);
INSERT INTO users(email,password_hash) VALUES('admin@example.com','legacy');
INSERT INTO domains(user_id,hostname,status) VALUES(1,'example.com','active');
INSERT INTO config(key,value) VALUES('domain','example.com');
INSERT INTO links(user_id,short_code,destination_url,domain,click_count) VALUES(1,'Docs','https://example.org','example.com',7);
INSERT INTO clicks(link_id,clicked_at,device,browser) VALUES(1,'2026-06-24 12:14:00','Desktop','Chrome');
INSERT INTO clicks(link_id,clicked_at,device,browser) VALUES(1,'2026-06-24 12:59:00','Mobile','Safari');
`
	if _, err := db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	migrate(db)

	for _, check := range []struct{ table, column string }{
		{"users", "password_changed_at"},
		{"domains", "cloudflare_token_enc"},
		{"domains", "is_default"},
		{"links", "redirect_type"},
		{"links", "notes"},
		{"links", "max_clicks"},
		{"links", "lifetime_click_count"},
		{"clicks", "visitor_hash"},
		{"clicks", "country_code"},
		{"clicks", "region_code"},
		{"clicks", "region_name"},
		{"clicks", "city"},
		{"clicks", "continent_code"},
		{"clicks", "latitude"},
		{"clicks", "longitude"},
	} {
		exists, err := sqliteColumnExists(db, check.table, check.column)
		if err != nil || !exists {
			t.Fatalf("missing migrated column %s.%s (err=%v)", check.table, check.column, err)
		}
	}
	var visibleClicks, lifetimeClicks int64
	if err := db.QueryRow(`SELECT click_count,lifetime_click_count FROM links WHERE short_code='Docs'`).Scan(&visibleClicks, &lifetimeClicks); err != nil {
		t.Fatal(err)
	}
	if visibleClicks != 7 || lifetimeClicks != 7 {
		t.Fatalf("migrated counters visible=%d lifetime=%d, want 7/7", visibleClicks, lifetimeClicks)
	}
	var backfilled int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(click_count),0) FROM click_rollups WHERE link_id=1`).Scan(&backfilled); err != nil {
		t.Fatal(err)
	}
	if backfilled != 2 {
		t.Fatalf("backfilled analytics rollup=%d, want 2 retained events", backfilled)
	}
	// The migration is idempotent and must not double historical clicks.
	migrate(db)
	if err := db.QueryRow(`SELECT COALESCE(SUM(click_count),0) FROM click_rollups WHERE link_id=1`).Scan(&backfilled); err != nil {
		t.Fatal(err)
	}
	if backfilled != 2 {
		t.Fatalf("second migration changed rollup to %d", backfilled)
	}

	var defaultCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domains WHERE is_default=1`).Scan(&defaultCount); err != nil || defaultCount != 1 {
		t.Fatalf("default domain migration count=%d err=%v", defaultCount, err)
	}
	if _, err := db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type) VALUES(1,'docs','https://example.net','example.com','slug')`); err != nil {
		t.Fatalf("case-distinct slug on the same domain was rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO domains(user_id,hostname,status,is_default) VALUES(1,'other.example.com','active',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type) VALUES(1,'Docs','https://other.example.net','other.example.com','slug')`); err != nil {
		t.Fatalf("same exact slug on a different domain was rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type) VALUES(1,'Docs','https://duplicate.example.net','example.com','slug')`); err == nil {
		t.Fatal("exact duplicate slug on the same domain was accepted")
	}
	for _, pragma := range []string{"secure_delete", "trusted_schema"} {
		var value int
		if err := db.QueryRow(`PRAGMA ` + pragma).Scan(&value); err != nil {
			t.Fatalf("read %s pragma: %v", pragma, err)
		}
		want := 1
		if pragma == "trusted_schema" {
			want = 0
		}
		if value != want {
			t.Fatalf("PRAGMA %s=%d, want %d", pragma, value, want)
		}
	}
}

func TestExplicitLegacyGlobalSlugIndexIsRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-explicit-index.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	legacy := `
CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT,email TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL,created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE domains (id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,hostname TEXT UNIQUE NOT NULL,status TEXT NOT NULL DEFAULT 'pending',message TEXT,cloudflare_zone_id TEXT,proxied INTEGER DEFAULT 0,created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE links (id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,short_code TEXT NOT NULL,destination_url TEXT NOT NULL,domain TEXT NOT NULL DEFAULT '',tag TEXT DEFAULT '',password_hash TEXT,expires_at DATETIME,utm_source TEXT,utm_medium TEXT,utm_campaign TEXT,status TEXT NOT NULL DEFAULT 'active',click_count INTEGER NOT NULL DEFAULT 0,created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE UNIQUE INDEX custom_global_slug_unique ON links(short_code COLLATE NOCASE);
CREATE TABLE clicks (id INTEGER PRIMARY KEY AUTOINCREMENT,link_id INTEGER NOT NULL,clicked_at DATETIME DEFAULT CURRENT_TIMESTAMP,referrer TEXT,user_agent TEXT,device TEXT,browser TEXT,ip TEXT);
INSERT INTO users(email,password_hash) VALUES('admin@example.com','legacy');
INSERT INTO domains(user_id,hostname,status) VALUES(1,'one.example.com','active');
INSERT INTO domains(user_id,hostname,status) VALUES(1,'two.example.com','active');
INSERT INTO config(key,value) VALUES('domain','one.example.com');
INSERT INTO links(user_id,short_code,destination_url,domain) VALUES(1,'Test','https://one.example','one.example.com');
`
	if _, err := db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	migrate(db)

	var oldIndex int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='custom_global_slug_unique'`).Scan(&oldIndex); err != nil {
		t.Fatal(err)
	}
	if oldIndex != 0 {
		t.Fatal("legacy explicit global slug index still exists")
	}
	if _, err := db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type) VALUES(1,'test','https://case.example','one.example.com','slug')`); err != nil {
		t.Fatalf("case-distinct slug was rejected after explicit-index migration: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO links(user_id,short_code,destination_url,domain,redirect_type) VALUES(1,'Test','https://two.example','two.example.com','slug')`); err != nil {
		t.Fatalf("cross-domain duplicate slug was rejected after explicit-index migration: %v", err)
	}
}
