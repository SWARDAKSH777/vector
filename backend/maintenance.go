package main

import (
	"strconv"
	"time"
)

func (s *server) runMaintenanceOnce(now time.Time) {
	now = now.UTC()
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`, now, now.Add(-sessionIdleTTL))
	analyticsDays := boundedConfigDays(s.getConfig("analytics_retention_days"), 90, 1, 3650)
	cutoff := now.AddDate(0, 0, -analyticsDays)
	// Detailed events contain pseudonymous visitor and audience dimensions and
	// obey the configured retention period. Anonymous hourly rollups contain no
	// visitor-level data and remain until the user explicitly clears analytics or
	// deletes the link, preserving accurate long-range totals.
	_, _ = s.db.Exec(`DELETE FROM analytics_events WHERE occurred_at < ?`, cutoff)
	_, _ = s.db.Exec(`DELETE FROM clicks WHERE clicked_at < ?`, cutoff)
	s.clearAnalyticsReportCache()
	auditDays := boundedConfigDays(s.getConfig("audit_retention_days"), 365, 30, 3650)
	_, _ = s.db.Exec(`DELETE FROM audit_logs WHERE created_at < ?`, now.AddDate(0, 0, -auditDays))
	_, _ = s.db.Exec(`DELETE FROM geo_country_cache WHERE expires_at <= ?`, now)
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
}

func (s *server) startMaintenance() {
	run := func() { s.runMaintenanceOnce(time.Now()) }
	run()
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()
}

func boundedConfigDays(raw string, fallback, min, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}
