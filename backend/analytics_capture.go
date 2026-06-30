package main

import (
	"database/sql"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type analyticsCapture struct {
	Enabled      bool
	ClientIP     string
	VisitorHash  string
	Referrer     string
	Device       string
	Browser      string
	OS           string
	Country      string
	CountryKnown bool
}

func (s *server) prepareAnalyticsCapture(r *http.Request) analyticsCapture {
	capture := analyticsCapture{}
	if s.getConfig("analytics_enabled") != "true" {
		return capture
	}
	capture.Enabled = true
	if privacySignalOptOut(r) {
		capture.Referrer = "Privacy-protected"
		capture.Device = "Privacy-protected"
		capture.Browser = "Privacy-protected"
		capture.OS = "Privacy-protected"
		return capture
	}
	classification := classifyClient(r)
	capture.Device = classification.Device
	capture.Browser = classification.Browser
	capture.OS = classification.OS
	capture.Referrer = referrerOrigin(r.Referer())
	capture.ClientIP = normalizedAnalyticsIP(analyticsClientIP(r))
	if capture.ClientIP != "" {
		seed := strings.Join([]string{capture.ClientIP, capture.Browser, capture.Device, capture.OS}, "|")
		capture.VisitorHash = stableValueHash(s.masterKey, "analytics-visitor-v2:"+seed)
	}
	if country, ok := trustedCloudflareCountry(r); ok {
		capture.Country, capture.CountryKnown = country, true
	} else if s.geo != nil && capture.ClientIP != "" {
		capture.Country, capture.CountryKnown = s.geo.cachedCountryForIP(capture.ClientIP)
	}
	return capture
}

func privacySignalOptOut(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get("Sec-GPC")) == "1" || strings.TrimSpace(r.Header.Get("DNT")) == "1"
}

func normalizedAnalyticsIP(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(strings.Trim(raw, "[]")))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func (s *server) insertAnalyticsEvent(tx *sql.Tx, linkID int64, occurredAt time.Time, capture analyticsCapture) int64 {
	if tx == nil || !capture.Enabled {
		return 0
	}
	if _, err := tx.Exec(`SAVEPOINT vector_analytics_event`); err != nil {
		s.recordAnalyticsCaptureFailure(err)
		return 0
	}
	res, err := tx.Exec(`INSERT INTO analytics_events(
		link_id,occurred_at,visitor_hash,referrer,device,browser,operating_system,country_code
	) VALUES(?,?,?,?,?,?,?,?)`, linkID, occurredAt.UTC(), capture.VisitorHash, capture.Referrer,
		capture.Device, capture.Browser, capture.OS, capture.Country)
	if err != nil {
		_, _ = tx.Exec(`ROLLBACK TO vector_analytics_event`)
		_, _ = tx.Exec(`RELEASE vector_analytics_event`)
		s.recordAnalyticsCaptureFailure(err)
		return 0
	}
	if _, err := tx.Exec(`RELEASE vector_analytics_event`); err != nil {
		s.recordAnalyticsCaptureFailure(err)
		return 0
	}
	id, err := res.LastInsertId()
	if err != nil {
		s.recordAnalyticsCaptureFailure(err)
		return 0
	}
	s.recordAnalyticsCaptureSuccess()
	return id
}

func (s *server) recordAnalyticsCaptureFailure(err error) {
	if s == nil || err == nil {
		return
	}
	s.analyticsHealthMu.Lock()
	s.analyticsCaptureFailures++
	s.analyticsLastError = safeGeoText(err.Error(), 240)
	s.analyticsLastErrorAt = time.Now().UTC()
	shouldLog := time.Since(s.analyticsLastErrorLogAt) > time.Minute
	if shouldLog {
		s.analyticsLastErrorLogAt = time.Now()
	}
	s.analyticsHealthMu.Unlock()
	if shouldLog {
		log.Printf("analytics event capture warning: %v", err)
	}
}

func (s *server) recordAnalyticsCaptureSuccess() {
	if s == nil {
		return
	}
	s.analyticsHealthMu.Lock()
	s.analyticsLastSuccessAt = time.Now().UTC()
	s.analyticsHealthMu.Unlock()
}

type analyticsCaptureHealth struct {
	Failures      uint64 `json:"failures"`
	Healthy       bool   `json:"healthy"`
	LastError     string `json:"last_error,omitempty"`
	LastErrorAt   string `json:"last_error_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
}

func (s *server) analyticsCaptureHealthSnapshot() analyticsCaptureHealth {
	s.analyticsHealthMu.RLock()
	defer s.analyticsHealthMu.RUnlock()
	out := analyticsCaptureHealth{
		Failures:  s.analyticsCaptureFailures,
		Healthy:   s.analyticsLastErrorAt.IsZero() || (!s.analyticsLastSuccessAt.IsZero() && s.analyticsLastSuccessAt.After(s.analyticsLastErrorAt)),
		LastError: s.analyticsLastError,
	}
	if !s.analyticsLastErrorAt.IsZero() {
		out.LastErrorAt = s.analyticsLastErrorAt.Format(time.RFC3339)
	}
	if !s.analyticsLastSuccessAt.IsZero() {
		out.LastSuccessAt = s.analyticsLastSuccessAt.Format(time.RFC3339)
	}
	return out
}
