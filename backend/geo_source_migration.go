package main

import (
	"database/sql"
	"errors"
	"fmt"
)

const geoCountrySourceVersion = "2"

// migrateGeoCountrySource invalidates only geography that may have been derived
// from a shared reverse-proxy edge address. It intentionally preserves every
// click, visitor hash, browser/device/OS value, referrer, timestamp, and rollup.
func (s *server) migrateGeoCountrySource() error {
	if s == nil || s.db == nil {
		return nil
	}
	var current string
	if err := s.db.QueryRow(`SELECT value FROM config WHERE key='geo_country_source_version'`).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read geo source migration version: %w", err)
	}
	if current == geoCountrySourceVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin geo source migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`UPDATE analytics_events SET country_code='' WHERE country_code<>''`,
		`UPDATE clicks SET country_code='' WHERE country_code<>''`,
		`DELETE FROM geo_country_cache`,
	} {
		if _, err = tx.Exec(statement); err != nil {
			return fmt.Errorf("clear stale geography: %w", err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO config(key,value) VALUES('geo_country_source_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, geoCountrySourceVersion); err != nil {
		return fmt.Errorf("record geo source migration: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit geo source migration: %w", err)
	}
	return nil
}
