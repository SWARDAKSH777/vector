package main

import (
	"context"
	"math"
	"net/http"
	"time"
)

func (s *server) handleAnalyticsReport(w http.ResponseWriter, r *http.Request) {
	filter, err := parseAnalyticsFilter(r, time.Now().UTC())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	uid := userIDFromCtx(r.Context())
	if !analyticsForceRefresh(r) {
		if report, ok := s.cachedAnalyticsReport(uid, filter); ok {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Vector-Analytics-Cache", "hit")
			writeJSON(w, http.StatusOK, report)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	report, err := s.loadAnalyticsReport(ctx, uid, filter)
	if err != nil {
		if context.Cause(ctx) != nil {
			writeErr(w, http.StatusServiceUnavailable, "analytics query timed out")
		} else {
			writeErr(w, http.StatusInternalServerError, "could not load analytics")
		}
		return
	}
	s.storeAnalyticsReport(uid, filter, report)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Vector-Analytics-Cache", "miss")
	writeJSON(w, http.StatusOK, report)
}

func (s *server) analyticsReportForCompatibility(w http.ResponseWriter, r *http.Request) (analyticsReport, bool) {
	filter, err := parseAnalyticsFilter(r, time.Now().UTC())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return analyticsReport{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	report, err := s.loadAnalyticsReport(ctx, userIDFromCtx(r.Context()), filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load analytics")
		return analyticsReport{}, false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	return report, true
}
func (s *server) handleStatsOverview(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.Overview)
	}
}
func (s *server) handleStatsTimeseries(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.Timeseries)
	}
}
func (s *server) handleStatsGeo(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.Geo)
	}
}
func (s *server) handleStatsDevices(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.Devices)
	}
}
func (s *server) handleStatsBrowsers(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.Browsers)
	}
}
func (s *server) handleStatsReferrers(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.Referrers)
	}
}
func (s *server) handleStatsTopLinks(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.TopLinks)
	}
}
func (s *server) handleStatsHours(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.analyticsReportForCompatibility(w, r); ok {
		writeJSON(w, 200, x.Hours)
	}
}
func (s *server) handleStatsOptions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, 500, "could not load analytics options")
		return
	}
	defer tx.Rollback()
	options, err := loadAnalyticsOptions(ctx, tx, userIDFromCtx(r.Context()))
	if err != nil {
		writeErr(w, 500, "could not load analytics options")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "could not load analytics options")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, options)
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
func repeatClickRate(clicks, unique int64) float64 {
	if clicks <= 0 || unique <= 0 || unique >= clicks {
		return 0
	}
	return round1(float64(clicks-unique) / float64(clicks) * 100)
}
