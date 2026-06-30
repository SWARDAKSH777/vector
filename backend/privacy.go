package main

import (
	"database/sql"
	"net/url"
)

func (s *server) migratePrivacyData() error {
	rows, err := s.db.Query(`SELECT id,ip,referrer FROM clicks WHERE (ip IS NOT NULL AND ip!='') OR (referrer IS NOT NULL AND referrer!='')`)
	if err != nil {
		return err
	}
	type item struct {
		id      int64
		ip, ref sql.NullString
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.ip, &it.ref); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, it := range items {
		visitor := ""
		if it.ip.Valid && it.ip.String != "" {
			visitor = stableValueHash(s.masterKey, "visitor:"+it.ip.String)
		}
		ref := ""
		if it.ref.Valid {
			if u, e := url.Parse(it.ref.String); e == nil && u.Scheme != "" && u.Host != "" {
				ref = u.Scheme + "://" + u.Host
			}
		}
		if _, err := s.db.Exec(`UPDATE clicks SET visitor_hash=COALESCE(NULLIF(visitor_hash,''),?),ip='',referrer=? WHERE id=?`, visitor, ref, it.id); err != nil {
			return err
		}
		// Analytics v2 preserves legacy event IDs during migration. Mirror the
		// privacy-safe visitor fingerprint and origin-only referrer so upgrades do
		// not lose unique-visitor data that can still be recovered safely.
		if _, err := s.db.Exec(`UPDATE analytics_events
			SET visitor_hash=COALESCE(NULLIF(visitor_hash,''),?), referrer=?
			WHERE id=?`, visitor, ref, it.id); err != nil {
			return err
		}
	}
	return nil
}
