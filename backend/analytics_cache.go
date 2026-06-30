package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const analyticsReportCacheTTL = 3 * time.Second
const analyticsReportCacheLimit = 128

type analyticsReportCacheEntry struct {
	Report    analyticsReport
	ExpiresAt time.Time
}

func analyticsReportCacheKey(uid int64, filter analyticsFilter) string {
	key, _ := json.Marshal(struct {
		UID      int64  `json:"u"`
		Range    string `json:"r"`
		LinkID   int64  `json:"l"`
		Country  string `json:"c"`
		Device   string `json:"d"`
		Browser  string `json:"b"`
		Referrer string `json:"f"`
	}{uid, filter.Range, filter.LinkID, filter.Country, filter.Device, filter.Browser, filter.Referrer})
	return string(key)
}

func (s *server) cachedAnalyticsReport(uid int64, filter analyticsFilter) (analyticsReport, bool) {
	if s == nil {
		return analyticsReport{}, false
	}
	key, now := analyticsReportCacheKey(uid, filter), time.Now()
	s.analyticsReportCacheMu.RLock()
	entry, ok := s.analyticsReportCache[key]
	s.analyticsReportCacheMu.RUnlock()
	if !ok || !now.Before(entry.ExpiresAt) {
		if ok {
			s.analyticsReportCacheMu.Lock()
			delete(s.analyticsReportCache, key)
			s.analyticsReportCacheMu.Unlock()
		}
		return analyticsReport{}, false
	}
	return entry.Report, true
}

func (s *server) storeAnalyticsReport(uid int64, filter analyticsFilter, report analyticsReport) {
	if s == nil {
		return
	}
	key := analyticsReportCacheKey(uid, filter)
	s.analyticsReportCacheMu.Lock()
	if s.analyticsReportCache == nil {
		s.analyticsReportCache = make(map[string]analyticsReportCacheEntry)
	}
	if len(s.analyticsReportCache) >= analyticsReportCacheLimit {
		now := time.Now()
		for k, v := range s.analyticsReportCache {
			if !now.Before(v.ExpiresAt) {
				delete(s.analyticsReportCache, k)
			}
		}
		for k := range s.analyticsReportCache {
			if len(s.analyticsReportCache) < analyticsReportCacheLimit {
				break
			}
			delete(s.analyticsReportCache, k)
		}
	}
	s.analyticsReportCache[key] = analyticsReportCacheEntry{Report: report, ExpiresAt: time.Now().Add(analyticsReportCacheTTL)}
	s.analyticsReportCacheMu.Unlock()
}

func (s *server) clearAnalyticsReportCache() {
	if s == nil {
		return
	}
	s.analyticsReportCacheMu.Lock()
	s.analyticsReportCache = make(map[string]analyticsReportCacheEntry)
	s.analyticsReportCacheMu.Unlock()
}

func analyticsForceRefresh(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.URL.Query().Get("refresh")) == "1" || strings.Contains(strings.ToLower(r.Header.Get("Cache-Control")), "no-cache")
}
