package main

import (
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	_ "urlshortener/sqlite3local"
)

func openDB(path string) *sql.DB {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	// Keep startup on one connection so schema rebuild PRAGMAs are deterministic.
	// After migration, a deliberately small pool minimizes RSS while WAL still
	// permits concurrent readers and one serialized writer.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}
	migrate(db)
	db.SetMaxOpenConns(intEnvBounded("DB_MAX_OPEN_CONNS", 2, 1, 8))
	db.SetMaxIdleConns(intEnvBounded("DB_MAX_IDLE_CONNS", 1, 0, 4))
	var integrity string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
		log.Fatalf("database integrity check failed: %v (%s)", err, integrity)
	}
	var foreignKeyProblems int
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		log.Fatalf("database foreign-key check failed: %v", err)
	}
	for rows.Next() {
		foreignKeyProblems++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		log.Fatalf("read database foreign-key check: %v", err)
	}
	if err := rows.Close(); err != nil {
		log.Fatalf("close database foreign-key check: %v", err)
	}
	if foreignKeyProblems > 0 {
		log.Fatalf("database contains %d foreign-key violation(s)", foreignKeyProblems)
	}
	return db
}

func migrate(db *sql.DB) {
	schema := `
CREATE TABLE IF NOT EXISTS config (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	email               TEXT UNIQUE NOT NULL,
	password_hash       TEXT NOT NULL,
	password_changed_at DATETIME,
	role                TEXT NOT NULL DEFAULT 'disabled',
	disabled            INTEGER NOT NULL DEFAULT 1,
	created_at          DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash      TEXT PRIMARY KEY,
	user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at      DATETIME NOT NULL,
	last_seen_at    DATETIME NOT NULL,
	expires_at      DATETIME NOT NULL,
	user_agent_hash TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS domains (
	id                       INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id                  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	hostname                 TEXT UNIQUE NOT NULL,
	status                   TEXT NOT NULL DEFAULT 'pending',
	message                  TEXT,
	cloudflare_zone_id       TEXT,
	cloudflare_token_enc     TEXT,
	proxied                  INTEGER DEFAULT 0,
	is_default               INTEGER NOT NULL DEFAULT 0,
	created_at               DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS links (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	short_code      TEXT NOT NULL,
	destination_url TEXT NOT NULL,
	domain          TEXT NOT NULL DEFAULT '',
	redirect_type   TEXT NOT NULL DEFAULT 'slug',
	tag             TEXT DEFAULT '',
	notes           TEXT DEFAULT '',
	password_hash   TEXT,
	expires_at      DATETIME,
	max_clicks      INTEGER,
	utm_source      TEXT,
	utm_medium      TEXT,
	utm_campaign    TEXT,
	status          TEXT NOT NULL DEFAULT 'active',
	click_count          INTEGER NOT NULL DEFAULT 0,
	lifetime_click_count INTEGER NOT NULL DEFAULT 0,
	created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS managed_dns_records (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	link_id     INTEGER UNIQUE REFERENCES links(id) ON DELETE CASCADE,
	domain_id   INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
	zone_id     TEXT NOT NULL,
	record_id   TEXT NOT NULL,
	hostname    TEXT NOT NULL,
	created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(zone_id, record_id)
);

CREATE TABLE IF NOT EXISTS click_rollups (
	link_id      INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
	bucket_hour  DATETIME NOT NULL,
	click_count  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(link_id, bucket_hour)
);

CREATE TABLE IF NOT EXISTS clicks (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	link_id      INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
	clicked_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
	referrer     TEXT,
	user_agent   TEXT,
	device       TEXT,
	browser      TEXT,
	ip           TEXT,
	visitor_hash  TEXT,
	country_code  TEXT NOT NULL DEFAULT '',
	region_code   TEXT NOT NULL DEFAULT '',
	region_name   TEXT NOT NULL DEFAULT '',
	city          TEXT NOT NULL DEFAULT '',
	continent_code TEXT NOT NULL DEFAULT '',
	latitude      REAL,
	longitude     REAL
);

-- Analytics v2 event store. This table intentionally contains no raw IP and
-- no full user-agent string. New click ingestion writes here atomically with
-- the aggregate counter. The legacy clicks table is retained only for migration.
CREATE TABLE IF NOT EXISTS analytics_events (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	link_id          INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
	occurred_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	visitor_hash     TEXT NOT NULL DEFAULT '',
	referrer         TEXT NOT NULL DEFAULT '',
	device           TEXT NOT NULL DEFAULT 'Unknown',
	browser          TEXT NOT NULL DEFAULT 'Unknown',
	operating_system TEXT NOT NULL DEFAULT 'Unknown',
	country_code     TEXT NOT NULL DEFAULT '',
	CHECK(length(visitor_hash) <= 128),
	CHECK(length(referrer) <= 512),
	CHECK(length(device) <= 32),
	CHECK(length(browser) <= 48),
	CHECK(length(operating_system) <= 48),
	CHECK(length(country_code) IN (0,2))
);

CREATE TABLE IF NOT EXISTS geo_country_cache (
	ip_hash       TEXT PRIMARY KEY,
	country_code  TEXT NOT NULL DEFAULT '',
	expires_at    DATETIME NOT NULL,
	last_used_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS audit_logs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	actor_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
	event       TEXT NOT NULL,
	target_type TEXT NOT NULL DEFAULT '',
	target_id   TEXT NOT NULL DEFAULT '',
	ip_hash     TEXT NOT NULL DEFAULT '',
	metadata    TEXT NOT NULL DEFAULT '{}',
	created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_clicks_link_id ON clicks(link_id);
CREATE INDEX IF NOT EXISTS idx_click_rollups_hour ON click_rollups(bucket_hour);
CREATE INDEX IF NOT EXISTS idx_clicks_clicked_at ON clicks(clicked_at);
CREATE INDEX IF NOT EXISTS idx_analytics_events_link_time ON analytics_events(link_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_analytics_events_time_link ON analytics_events(occurred_at, link_id);
CREATE INDEX IF NOT EXISTS idx_analytics_events_visitor_time ON analytics_events(visitor_hash, occurred_at) WHERE visitor_hash<>'';
CREATE INDEX IF NOT EXISTS idx_analytics_events_country_time ON analytics_events(country_code, occurred_at) WHERE country_code<>'';
CREATE INDEX IF NOT EXISTS idx_analytics_events_device_time ON analytics_events(device, occurred_at);
CREATE INDEX IF NOT EXISTS idx_analytics_events_browser_time ON analytics_events(browser, occurred_at);
CREATE INDEX IF NOT EXISTS idx_links_user_id ON links(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_geo_country_cache_expires ON geo_country_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor_id ON audit_logs(actor_id);
CREATE INDEX IF NOT EXISTS idx_managed_dns_link_id ON managed_dns_records(link_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_managed_dns_domain_hostname ON managed_dns_records(domain_id, hostname);
`
	if _, err := db.Exec(schema); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	addCols := []struct {
		table, column, ddl string
	}{
		{"users", "password_changed_at", `ALTER TABLE users ADD COLUMN password_changed_at DATETIME`},
		{"users", "role", `ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'disabled'`},
		{"users", "disabled", `ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 1`},
		{"links", "redirect_type", `ALTER TABLE links ADD COLUMN redirect_type TEXT NOT NULL DEFAULT 'slug'`},
		{"links", "notes", `ALTER TABLE links ADD COLUMN notes TEXT DEFAULT ''`},
		{"links", "max_clicks", `ALTER TABLE links ADD COLUMN max_clicks INTEGER`},
		{"domains", "cloudflare_token_enc", `ALTER TABLE domains ADD COLUMN cloudflare_token_enc TEXT`},
		{"domains", "is_default", `ALTER TABLE domains ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`},
		{"clicks", "visitor_hash", `ALTER TABLE clicks ADD COLUMN visitor_hash TEXT`},
		{"links", "lifetime_click_count", `ALTER TABLE links ADD COLUMN lifetime_click_count INTEGER NOT NULL DEFAULT 0`},
		{"clicks", "country_code", `ALTER TABLE clicks ADD COLUMN country_code TEXT NOT NULL DEFAULT ''`},
		{"clicks", "region_code", `ALTER TABLE clicks ADD COLUMN region_code TEXT NOT NULL DEFAULT ''`},
		{"clicks", "region_name", `ALTER TABLE clicks ADD COLUMN region_name TEXT NOT NULL DEFAULT ''`},
		{"clicks", "city", `ALTER TABLE clicks ADD COLUMN city TEXT NOT NULL DEFAULT ''`},
		{"clicks", "continent_code", `ALTER TABLE clicks ADD COLUMN continent_code TEXT NOT NULL DEFAULT ''`},
		{"clicks", "latitude", `ALTER TABLE clicks ADD COLUMN latitude REAL`},
		{"clicks", "longitude", `ALTER TABLE clicks ADD COLUMN longitude REAL`},
	}
	for _, migration := range addCols {
		exists, err := sqliteColumnExists(db, migration.table, migration.column)
		if err != nil {
			log.Fatalf("inspect schema for %s.%s: %v", migration.table, migration.column, err)
		}
		if !exists {
			if _, err := db.Exec(migration.ddl); err != nil {
				log.Fatalf("add schema column %s.%s: %v", migration.table, migration.column, err)
			}
		}
	}

	if err := migrateScopedCaseSensitiveSlugs(db); err != nil {
		log.Fatalf("migrate domain-scoped case-sensitive slugs: %v", err)
	}

	if _, err := db.Exec(`UPDATE users SET password_changed_at=COALESCE(password_changed_at, created_at, CURRENT_TIMESTAMP)`); err != nil {
		log.Fatalf("backfill password change timestamps: %v", err)
	}
	var authzMigration string
	authzErr := db.QueryRow(`SELECT value FROM config WHERE key='admin_authorization_v1'`).Scan(&authzMigration)
	if authzErr == sql.ErrNoRows {
		tx, err := db.Begin()
		if err != nil {
			log.Fatalf("begin authorization migration: %v", err)
		}
		if _, err = tx.Exec(`UPDATE users SET role='disabled', disabled=1`); err == nil {
			_, err = tx.Exec(`UPDATE users SET role='admin', disabled=0 WHERE id=(SELECT MIN(id) FROM users)`)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM sessions WHERE user_id NOT IN (SELECT id FROM users WHERE role='admin' AND disabled=0)`)
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO config(key,value) VALUES('admin_authorization_v1','done')`)
		}
		if err != nil {
			_ = tx.Rollback()
			log.Fatalf("apply authorization migration: %v", err)
		}
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit authorization migration: %v", err)
		}
	} else if authzErr != nil {
		log.Fatalf("inspect authorization migration state: %v", authzErr)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_single_admin ON users(role) WHERE role='admin' AND disabled=0`); err != nil {
		log.Fatalf("enforce one active administrator: %v", err)
	}
	if err := migrateAnalyticsV2(db); err != nil {
		log.Fatalf("migrate analytics v2: %v", err)
	}
	// Preserve the historical redirect count for click-limit enforcement while
	// keeping click_count resettable as the user-visible analytics counter.
	if _, err := db.Exec(`UPDATE links SET lifetime_click_count=click_count WHERE lifetime_click_count=0 AND click_count>0`); err != nil {
		log.Fatalf("backfill lifetime click counts: %v", err)
	}
	// Seed hourly aggregates from retained historical events once. This keeps
	// upgraded analytics charts consistent with the detailed history that still
	// exists, without inventing timestamps for privacy-opted-out or expired data.
	var rollupBackfill string
	backfillErr := db.QueryRow(`SELECT value FROM config WHERE key='analytics_rollup_backfill_v1'`).Scan(&rollupBackfill)
	if backfillErr == sql.ErrNoRows {
		if _, err := db.Exec(`INSERT INTO click_rollups(link_id,bucket_hour,click_count)
			SELECT link_id, strftime('%Y-%m-%d %H:00:00', clicked_at), COUNT(*)
			FROM clicks
			GROUP BY link_id, strftime('%Y-%m-%d %H:00:00', clicked_at)
			ON CONFLICT(link_id,bucket_hour) DO NOTHING`); err != nil {
			log.Fatalf("backfill analytics rollups: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO config(key,value) VALUES('analytics_rollup_backfill_v1','done')
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
			log.Fatalf("mark analytics rollup backfill: %v", err)
		}
	} else if backfillErr != nil {
		log.Fatalf("inspect analytics rollup backfill state: %v", backfillErr)
	}
	if _, err := db.Exec(`UPDATE domains SET is_default=1
		WHERE hostname=(SELECT value FROM config WHERE key='domain')
		  AND NOT EXISTS (SELECT 1 FROM domains WHERE is_default=1)`); err != nil {
		log.Fatalf("restore configured default domain: %v", err)
	}
	if _, err := db.Exec(`UPDATE domains SET is_default=1
		WHERE id=(SELECT id FROM domains WHERE status='active' ORDER BY created_at ASC LIMIT 1)
		  AND NOT EXISTS (SELECT 1 FROM domains WHERE is_default=1)`); err != nil {
		log.Fatalf("select fallback default domain: %v", err)
	}
	if _, err := db.Exec(`UPDATE domains SET is_default=0
		WHERE is_default=1 AND id NOT IN (SELECT MIN(id) FROM domains WHERE is_default=1 GROUP BY user_id)`); err != nil {
		log.Fatalf("deduplicate default domains: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_one_default_per_user
		ON domains(user_id) WHERE is_default=1`); err != nil {
		log.Fatalf("enforce one default domain per user: %v", err)
	}
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_links_short_code_nocase`); err != nil {
		log.Fatalf("remove legacy global short-code index: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_links_domain_type_code
		ON links(domain COLLATE NOCASE, redirect_type, short_code COLLATE BINARY)`); err != nil {
		log.Fatalf("enforce domain-scoped case-sensitive short-code uniqueness: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_links_user_id ON links(user_id)`); err != nil {
		log.Fatalf("create link owner index: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_hostname_nocase
		ON domains(hostname COLLATE NOCASE)`); err != nil {
		log.Fatalf("enforce case-insensitive domain uniqueness: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_clicks_visitor_hash ON clicks(visitor_hash)`); err != nil {
		log.Fatalf("create visitor-hash index: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_clicks_country_time ON clicks(country_code, clicked_at)`); err != nil {
		log.Fatalf("create click country/time index: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_clicks_link_time ON clicks(link_id, clicked_at)`); err != nil {
		log.Fatalf("create click link/time index: %v", err)
	}

	defaults := map[string]string{
		"analytics_enabled":        "true",
		"analytics_retention_days": "90",
		"audit_retention_days":     "365",
	}
	for key, value := range defaults {
		if _, err := db.Exec(`INSERT OR IGNORE INTO config(key,value) VALUES(?,?)`, key, value); err != nil {
			log.Fatalf("seed configuration default %s: %v", key, err)
		}
	}

	// Expired sessions are operational data, not audit evidence. Cleanup is not
	// required for schema correctness, but a database write failure at startup is
	// a useful signal that the service should not accept traffic.
	if _, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP`); err != nil {
		log.Fatalf("remove expired sessions during startup: %v", err)
	}
}

// migrateScopedCaseSensitiveSlugs removes the legacy global UNIQUE constraint
// on links.short_code. SQLite cannot drop a column-level UNIQUE constraint and
// legacy builds may also have an explicit one-column unique index, so
// upgraded databases are rebuilt in-place with foreign-key checks disabled on
// the single startup connection. IDs are preserved, therefore click history and
// managed DNS ownership remain attached to the same links.
func migrateScopedCaseSensitiveSlugs(db *sql.DB) error {
	needsRebuild, err := linksShortCodeHasGlobalUniqueConstraint(db)
	if err != nil {
		return err
	}
	if needsRebuild {
		if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			return err
		}
		reenable := func() error {
			_, err := db.Exec(`PRAGMA foreign_keys=ON`)
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			_ = reenable()
			return err
		}
		statements := []string{
			`DROP TABLE IF EXISTS links_scoped_slug_migration`,
			`CREATE TABLE links_scoped_slug_migration (
				id                   INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id              INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				short_code           TEXT NOT NULL,
				destination_url      TEXT NOT NULL,
				domain               TEXT NOT NULL DEFAULT '',
				redirect_type        TEXT NOT NULL DEFAULT 'slug',
				tag                  TEXT DEFAULT '',
				notes                TEXT DEFAULT '',
				password_hash        TEXT,
				expires_at           DATETIME,
				max_clicks           INTEGER,
				utm_source           TEXT,
				utm_medium           TEXT,
				utm_campaign         TEXT,
				status               TEXT NOT NULL DEFAULT 'active',
				click_count          INTEGER NOT NULL DEFAULT 0,
				lifetime_click_count INTEGER NOT NULL DEFAULT 0,
				created_at           DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
			`INSERT INTO links_scoped_slug_migration(
				id,user_id,short_code,destination_url,domain,redirect_type,tag,notes,
				password_hash,expires_at,max_clicks,utm_source,utm_medium,utm_campaign,
				status,click_count,lifetime_click_count,created_at
			) SELECT
				id,user_id,short_code,destination_url,domain,redirect_type,tag,notes,
				password_hash,expires_at,max_clicks,utm_source,utm_medium,utm_campaign,
				status,click_count,lifetime_click_count,created_at
			FROM links`,
			`DROP TABLE links`,
			`ALTER TABLE links_scoped_slug_migration RENAME TO links`,
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement); err != nil {
				_ = tx.Rollback()
				_ = reenable()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			_ = reenable()
			return err
		}
		if err := reenable(); err != nil {
			return err
		}
	}

	// Domains are DNS names and therefore case-insensitive. Existing releases
	// already normalized input, but normalize legacy/manual rows before creating
	// the NOCASE unique index. Refuse ambiguous data instead of silently merging.
	var duplicate string
	err = db.QueryRow(`SELECT lower(hostname) FROM domains GROUP BY lower(hostname) HAVING COUNT(*)>1 LIMIT 1`).Scan(&duplicate)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		return &simpleErr{"duplicate domains differ only by letter case: " + duplicate}
	}
	if _, err := db.Exec(`UPDATE domains SET hostname=lower(trim(hostname))`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE links SET domain=lower(trim(domain))`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE config SET value=lower(trim(value)) WHERE key='domain'`); err != nil {
		return err
	}
	return nil
}

func linksShortCodeHasGlobalUniqueConstraint(db *sql.DB) (bool, error) {
	rows, err := db.Query(`PRAGMA index_list(links)`)
	if err != nil {
		return false, err
	}
	var candidates []string
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return false, err
		}
		if unique == 1 {
			candidates = append(candidates, name)
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, name := range candidates {
		info, err := db.Query(`PRAGMA index_info('` + strings.ReplaceAll(name, `'`, `''`) + `')`)
		if err != nil {
			return false, err
		}
		columns := make([]string, 0, 2)
		for info.Next() {
			var seqno, cid int
			var column string
			if err := info.Scan(&seqno, &cid, &column); err != nil {
				_ = info.Close()
				return false, err
			}
			columns = append(columns, column)
		}
		if err := info.Close(); err != nil {
			return false, err
		}
		if len(columns) == 1 && columns[0] == "short_code" {
			return true, nil
		}
	}
	return false, nil
}

func sqliteColumnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func migrateAnalyticsV2(db *sql.DB) error {
	var done string
	err := db.QueryRow(`SELECT value FROM config WHERE key='analytics_v2_event_migration'`).Scan(&done)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`INSERT INTO analytics_events(
		id,link_id,occurred_at,visitor_hash,referrer,device,browser,operating_system,country_code
	)
	SELECT id,link_id,clicked_at,COALESCE(visitor_hash,''),COALESCE(referrer,''),
		COALESCE(NULLIF(device,''),'Unknown'),COALESCE(NULLIF(browser,''),'Unknown'),
		'Unknown',COALESCE(country_code,'')
	FROM clicks`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO config(key,value) VALUES('analytics_v2_event_migration','done')`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *server) getConfigE(key string) (string, error) {
	if value, ok := s.getCachedConfig(key); ok {
		return value, nil
	}
	var value string
	err := s.db.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		// Missing keys are stable defaults and may be cached. Transient database
		// failures must not poison the runtime cache with an empty security or
		// routing setting.
		s.storeCachedConfig(key, value)
		return value, nil
	}
	return "", err
}

func (s *server) getConfig(key string) string {
	value, _ := s.getConfigE(key)
	return value
}

func (s *server) setConfigE(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO config(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err == nil {
		s.storeCachedConfig(key, value)
	}
	return err
}

func (s *server) setConfig(key, value string) { _ = s.setConfigE(key, value) }
