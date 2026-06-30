package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	analyticsDefaultRange   = "30d"
	analyticsMaxFacetItems  = 100
	analyticsMaxLinkOptions = 500
)

type analyticsFilter struct {
	Range    string
	Days     int
	All      bool
	Start    time.Time
	End      time.Time
	LinkID   int64
	Country  string
	Device   string
	Browser  string
	Referrer string
}

type analyticsFilterPayload struct {
	Range    string `json:"range"`
	LinkID   int64  `json:"link_id,omitempty"`
	Country  string `json:"country,omitempty"`
	Device   string `json:"device,omitempty"`
	Browser  string `json:"browser,omitempty"`
	Referrer string `json:"referrer,omitempty"`
}

type analyticsOverview struct {
	TotalClicks              int64   `json:"total_clicks"`
	DetailedClicks           int64   `json:"detailed_clicks"`
	AllTimeClicks            int64   `json:"all_time_clicks"`
	TotalClicksDelta         float64 `json:"total_clicks_delta"`
	DeltaAvailable           bool    `json:"delta_available"`
	UniqueVisitors           int64   `json:"unique_visitors"`
	ActiveLinks              int64   `json:"active_links"`
	AverageDailyClicks       float64 `json:"avg_daily_clicks"`
	RepeatClickRate          float64 `json:"repeat_click_rate"`
	DetailedCoverage         float64 `json:"detailed_coverage"`
	AnalyticsEnabled         bool    `json:"analytics_enabled"`
	AnalyticsRetentionDays   int     `json:"analytics_retention_days"`
	GeoProvider              string  `json:"geo_provider"`
	GeoProviderConfigured    bool    `json:"geo_provider_configured"`
	GeoFallbackConfigured    bool    `json:"geo_fallback_configured"`
	GeoQueueDepth            int     `json:"geo_queue_depth"`
	GeoCacheEntries          int64   `json:"geo_cache_entries"`
	RangeDays                int     `json:"range_days"`
	RangeStart               string  `json:"range_start"`
	RangeEnd                 string  `json:"range_end"`
	DataMode                 string  `json:"data_mode"`
	AllTimeIgnoresDimensions bool    `json:"all_time_ignores_dimensions"`
}

type analyticsTimeseriesPoint struct {
	Date   string `json:"date"`
	Day    string `json:"day"`
	Clicks int64  `json:"clicks"`
	Unique int64  `json:"unique"`
}
type analyticsGroupedStat struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Clicks int64   `json:"clicks"`
	Unique int64   `json:"unique"`
}
type analyticsReferrerStat struct {
	Source string `json:"source"`
	Clicks int64  `json:"clicks"`
	Unique int64  `json:"unique"`
}
type analyticsCountryStat struct {
	Code   string `json:"code"`
	Clicks int64  `json:"clicks"`
	Unique int64  `json:"unique"`
}
type analyticsGeoResponse struct {
	Countries     []analyticsCountryStat `json:"countries"`
	LocatedClicks int64                  `json:"located_clicks"`
	TotalClicks   int64                  `json:"total_clicks"`
	Coverage      float64                `json:"coverage"`
}
type analyticsTopLinkStat struct {
	ID             int64  `json:"id"`
	ShortCode      string `json:"short_code"`
	Domain         string `json:"domain"`
	RedirectType   string `json:"redirect_type"`
	DestinationURL string `json:"destination_url"`
	Clicks         int64  `json:"clicks"`
	Unique         int64  `json:"unique"`
	AllTimeClicks  int64  `json:"all_time_clicks"`
}
type analyticsHourlyStat struct {
	Hour   int   `json:"hour"`
	Clicks int64 `json:"clicks"`
}
type analyticsLinkOption struct {
	ID        int64  `json:"id"`
	ShortCode string `json:"short_code"`
	Domain    string `json:"domain"`
}
type analyticsRegionOption struct {
	CountryCode string `json:"country_code"`
	Code        string `json:"code"`
	Name        string `json:"name"`
}
type analyticsOptions struct {
	Links     []analyticsLinkOption   `json:"links"`
	Countries []string                `json:"countries"`
	Regions   []analyticsRegionOption `json:"regions"`
	Devices   []string                `json:"devices"`
	Browsers  []string                `json:"browsers"`
	Referrers []string                `json:"referrers"`
}

type analyticsReport struct {
	GeneratedAt   string                     `json:"generated_at"`
	SchemaVersion int                        `json:"schema_version"`
	Granularity   string                     `json:"granularity"`
	Filters       analyticsFilterPayload     `json:"filters"`
	Overview      analyticsOverview          `json:"overview"`
	Timeseries    []analyticsTimeseriesPoint `json:"timeseries"`
	Geo           analyticsGeoResponse       `json:"geo"`
	Devices       []analyticsGroupedStat     `json:"devices"`
	Browsers      []analyticsGroupedStat     `json:"browsers"`
	Referrers     []analyticsReferrerStat    `json:"referrers"`
	TopLinks      []analyticsTopLinkStat     `json:"top_links"`
	Hours         []analyticsHourlyStat      `json:"hours"`
	Options       analyticsOptions           `json:"options"`
	Capture       analyticsCaptureHealth     `json:"capture"`
}

func parseAnalyticsFilter(r *http.Request, now time.Time) (analyticsFilter, error) {
	now = now.UTC()
	q := r.URL.Query()
	rangeKey := strings.TrimSpace(q.Get("range"))
	if rangeKey == "" {
		rangeKey = analyticsDefaultRange
	}
	f := analyticsFilter{Range: rangeKey, End: now}
	switch rangeKey {
	case "7d":
		f.Days = 7
	case "30d":
		f.Days = 30
	case "90d":
		f.Days = 90
	case "1y":
		f.Days = 365
	case "all":
		f.All = true
	default:
		return f, errors.New("invalid analytics range")
	}
	if !f.All {
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		f.Start = dayStart.AddDate(0, 0, -(f.Days - 1))
	}
	if raw := strings.TrimSpace(q.Get("link_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return f, errors.New("invalid link filter")
		}
		f.LinkID = id
	}
	if raw := strings.TrimSpace(q.Get("country")); raw != "" {
		country := strings.ToUpper(raw)
		if !validCountryCode(country) {
			return f, errors.New("invalid country filter")
		}
		f.Country = country
	}
	if raw := strings.TrimSpace(q.Get("region")); raw != "" {
		return f, errors.New("region analytics is not available")
	}
	var err error
	if f.Device, err = analyticsTextFilter(q.Get("device"), 32, "device"); err != nil {
		return f, err
	}
	if f.Browser, err = analyticsTextFilter(q.Get("browser"), 48, "browser"); err != nil {
		return f, err
	}
	if f.Referrer, err = analyticsTextFilter(q.Get("referrer"), 512, "referrer"); err != nil {
		return f, err
	}
	return f, nil
}

func analyticsTextFilter(raw string, limit int, name string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > limit {
		return "", fmt.Errorf("%s filter is too long", name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s filter contains invalid characters", name)
		}
	}
	return value, nil
}

func (f analyticsFilter) payload() analyticsFilterPayload {
	return analyticsFilterPayload{Range: f.Range, LinkID: f.LinkID, Country: f.Country, Device: f.Device, Browser: f.Browser, Referrer: f.Referrer}
}
func (f analyticsFilter) usesEventDimensions() bool {
	return f.Country != "" || f.Device != "" || f.Browser != "" || f.Referrer != ""
}

func (f analyticsFilter) eventWhere(uid int64, start, end *time.Time) (string, []any) {
	clauses := []string{"l.user_id=?"}
	args := []any{uid}
	if start != nil {
		clauses = append(clauses, "c.occurred_at>=?")
		args = append(args, *start)
	}
	if end != nil {
		clauses = append(clauses, "c.occurred_at<?")
		args = append(args, *end)
	}
	if f.LinkID > 0 {
		clauses = append(clauses, "l.id=?")
		args = append(args, f.LinkID)
	}
	if f.Country != "" {
		clauses = append(clauses, "c.country_code=?")
		args = append(args, f.Country)
	}
	if f.Device != "" {
		clauses = append(clauses, "COALESCE(NULLIF(c.device,''),'Unknown')=?")
		args = append(args, f.Device)
	}
	if f.Browser != "" {
		clauses = append(clauses, "COALESCE(NULLIF(c.browser,''),'Unknown')=?")
		args = append(args, f.Browser)
	}
	if f.Referrer != "" {
		clauses = append(clauses, "COALESCE(NULLIF(c.referrer,''),'Direct')=?")
		args = append(args, f.Referrer)
	}
	return strings.Join(clauses, " AND "), args
}
func (f analyticsFilter) rollupWhere(uid int64, start, end *time.Time) (string, []any) {
	clauses := []string{"l.user_id=?"}
	args := []any{uid}
	if start != nil {
		clauses = append(clauses, "cr.bucket_hour>=?")
		args = append(args, *start)
	}
	if end != nil {
		clauses = append(clauses, "cr.bucket_hour<?")
		args = append(args, *end)
	}
	if f.LinkID > 0 {
		clauses = append(clauses, "l.id=?")
		args = append(args, f.LinkID)
	}
	return strings.Join(clauses, " AND "), args
}

func (s *server) loadAnalyticsReport(ctx context.Context, uid int64, filter analyticsFilter) (analyticsReport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return analyticsReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if filter.All {
		filter.Start, err = resolveAnalyticsStart(ctx, tx, uid, filter)
		if err != nil {
			return analyticsReport{}, err
		}
		filter.Days = analyticsCalendarDays(filter.Start, filter.End)
	}
	if filter.Start.After(filter.End) {
		filter.Start = filter.End
	}
	granularity := analyticsGranularity(filter.Start, filter.End)
	report := analyticsReport{GeneratedAt: filter.End.Format(time.RFC3339), SchemaVersion: 2, Granularity: granularity, Filters: filter.payload(), Capture: s.analyticsCaptureHealthSnapshot()}
	if report.Overview, err = s.loadAnalyticsOverview(ctx, tx, uid, filter); err != nil {
		return analyticsReport{}, fmt.Errorf("overview: %w", err)
	}
	if report.Timeseries, err = loadAnalyticsTimeseries(ctx, tx, uid, filter, granularity); err != nil {
		return analyticsReport{}, fmt.Errorf("timeseries: %w", err)
	}
	if report.Geo, err = loadAnalyticsGeo(ctx, tx, uid, filter); err != nil {
		return analyticsReport{}, fmt.Errorf("geography: %w", err)
	}
	if report.Devices, err = loadAnalyticsGrouped(ctx, tx, uid, filter, "device"); err != nil {
		return analyticsReport{}, fmt.Errorf("devices: %w", err)
	}
	if report.Browsers, err = loadAnalyticsGrouped(ctx, tx, uid, filter, "browser"); err != nil {
		return analyticsReport{}, fmt.Errorf("browsers: %w", err)
	}
	if report.Referrers, err = loadAnalyticsReferrers(ctx, tx, uid, filter); err != nil {
		return analyticsReport{}, fmt.Errorf("referrers: %w", err)
	}
	if report.TopLinks, err = loadAnalyticsTopLinks(ctx, tx, uid, filter); err != nil {
		return analyticsReport{}, fmt.Errorf("top links: %w", err)
	}
	if report.Hours, err = loadAnalyticsHours(ctx, tx, uid, filter); err != nil {
		return analyticsReport{}, fmt.Errorf("hours: %w", err)
	}
	if report.Options, err = loadAnalyticsOptions(ctx, tx, uid); err != nil {
		return analyticsReport{}, fmt.Errorf("options: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return analyticsReport{}, err
	}
	return report, nil
}

func resolveAnalyticsStart(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter) (time.Time, error) {
	var raw sql.NullString
	if filter.usesEventDimensions() {
		where, args := filter.eventWhere(uid, nil, &filter.End)
		if err := tx.QueryRowContext(ctx, `SELECT MIN(c.occurred_at) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+where, args...).Scan(&raw); err != nil {
			return time.Time{}, err
		}
	} else {
		where, args := filter.rollupWhere(uid, nil, &filter.End)
		if err := tx.QueryRowContext(ctx, `SELECT MIN(cr.bucket_hour) FROM click_rollups cr JOIN links l ON l.id=cr.link_id WHERE `+where, args...).Scan(&raw); err != nil {
			return time.Time{}, err
		}
	}
	if raw.Valid {
		value, ok := parseAnalyticsTimestamp(raw.String)
		if !ok {
			return time.Time{}, errors.New("invalid analytics timestamp")
		}
		value = value.UTC()
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Date(filter.End.Year(), filter.End.Month(), filter.End.Day(), 0, 0, 0, 0, time.UTC), nil
}

func (s *server) loadAnalyticsOverview(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter) (analyticsOverview, error) {
	var out analyticsOverview
	where, args := filter.eventWhere(uid, &filter.Start, &filter.End)
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(DISTINCT NULLIF(c.visitor_hash,'')) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+where, args...).Scan(&out.DetailedClicks, &out.UniqueVisitors); err != nil {
		return out, err
	}
	if filter.usesEventDimensions() {
		out.TotalClicks = out.DetailedClicks
		out.DataMode = "detailed"
	} else {
		rw, ra := filter.rollupWhere(uid, &filter.Start, &filter.End)
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(cr.click_count),0) FROM click_rollups cr JOIN links l ON l.id=cr.link_id WHERE `+rw, ra...).Scan(&out.TotalClicks); err != nil {
			return out, err
		}
		out.DataMode = "aggregate"
	}
	linkWhere := "user_id=?"
	linkArgs := []any{uid}
	if filter.LinkID > 0 {
		linkWhere += " AND id=?"
		linkArgs = append(linkArgs, filter.LinkID)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(click_count),0) FROM links WHERE `+linkWhere, linkArgs...).Scan(&out.AllTimeClicks); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM links WHERE user_id=? AND status='active'`, uid).Scan(&out.ActiveLinks); err != nil {
		return out, err
	}
	if !filter.All {
		duration := filter.End.Sub(filter.Start)
		ps, pe := filter.Start.Add(-duration), filter.Start
		var previous int64
		if filter.usesEventDimensions() {
			pw, pa := filter.eventWhere(uid, &ps, &pe)
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+pw, pa...).Scan(&previous); err != nil {
				return out, err
			}
		} else {
			pw, pa := filter.rollupWhere(uid, &ps, &pe)
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(cr.click_count),0) FROM click_rollups cr JOIN links l ON l.id=cr.link_id WHERE `+pw, pa...).Scan(&previous); err != nil {
				return out, err
			}
		}
		out.DeltaAvailable = previous > 0 || out.TotalClicks > 0
		if previous > 0 {
			out.TotalClicksDelta = round1((float64(out.TotalClicks) - float64(previous)) / float64(previous) * 100)
		} else if out.TotalClicks > 0 {
			out.TotalClicksDelta = 100
		}
	}
	out.RangeDays = analyticsCalendarDays(filter.Start, filter.End)
	if out.RangeDays > 0 {
		out.AverageDailyClicks = round1(float64(out.TotalClicks) / float64(out.RangeDays))
	}
	out.RepeatClickRate = repeatClickRate(out.DetailedClicks, out.UniqueVisitors)
	if out.TotalClicks > 0 {
		out.DetailedCoverage = round1(float64(out.DetailedClicks) / float64(out.TotalClicks) * 100)
	}
	ae, err := analyticsConfigValue(ctx, tx, "analytics_enabled")
	if err != nil {
		return out, err
	}
	ret, err := analyticsConfigValue(ctx, tx, "analytics_retention_days")
	if err != nil {
		return out, err
	}
	out.AnalyticsEnabled = ae == "true"
	out.AnalyticsRetentionDays = boundedConfigDays(ret, 90, 1, 3650)
	out.GeoProvider = "cloudflare_cf_ipcountry"
	out.GeoProviderConfigured = true
	if s.geo != nil {
		out.GeoFallbackConfigured = s.geo.configured()
		if out.GeoFallbackConfigured {
			out.GeoProvider = "cloudflare_cf_ipcountry+ipinfo_lite"
		}
		out.GeoQueueDepth = s.geo.queueDepth()
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM geo_country_cache WHERE expires_at>CURRENT_TIMESTAMP`).Scan(&out.GeoCacheEntries); err != nil {
		return out, err
	}
	out.RangeStart = filter.Start.Format("2006-01-02")
	out.RangeEnd = filter.End.Format(time.RFC3339)
	out.AllTimeIgnoresDimensions = filter.usesEventDimensions()
	return out, nil
}
func analyticsConfigValue(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM config WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func loadAnalyticsTimeseries(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter, granularity string) ([]analyticsTimeseriesPoint, error) {
	type values struct{ clicks, unique int64 }
	by := map[string]values{}
	er := analyticsBucketExpression("cr.bucket_hour", granularity)
	ee := analyticsBucketExpression("c.occurred_at", granularity)
	if filter.usesEventDimensions() {
		w, a := filter.eventWhere(uid, &filter.Start, &filter.End)
		rows, err := tx.QueryContext(ctx, `SELECT `+ee+`,COUNT(*),COUNT(DISTINCT NULLIF(c.visitor_hash,'')) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+w+` GROUP BY 1 ORDER BY 1`, a...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			var v values
			if err := rows.Scan(&k, &v.clicks, &v.unique); err != nil {
				rows.Close()
				return nil, err
			}
			by[k] = v
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	} else {
		w, a := filter.rollupWhere(uid, &filter.Start, &filter.End)
		rows, err := tx.QueryContext(ctx, `SELECT `+er+`,COALESCE(SUM(cr.click_count),0) FROM click_rollups cr JOIN links l ON l.id=cr.link_id WHERE `+w+` GROUP BY 1 ORDER BY 1`, a...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			var c int64
			if err := rows.Scan(&k, &c); err != nil {
				rows.Close()
				return nil, err
			}
			v := by[k]
			v.clicks = c
			by[k] = v
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		ew, ea := filter.eventWhere(uid, &filter.Start, &filter.End)
		rows, err = tx.QueryContext(ctx, `SELECT `+ee+`,COUNT(DISTINCT NULLIF(c.visitor_hash,'')) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+ew+` GROUP BY 1 ORDER BY 1`, ea...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			var u int64
			if err := rows.Scan(&k, &u); err != nil {
				rows.Close()
				return nil, err
			}
			v := by[k]
			v.unique = u
			by[k] = v
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	start := analyticsBucketStart(filter.Start, granularity)
	end := analyticsBucketStart(filter.End, granularity)
	if end.Before(filter.End) {
		end = analyticsNextBucket(end, granularity)
	}
	out := make([]analyticsTimeseriesPoint, 0, 128)
	for cur := start; cur.Before(end) && len(out) < 4000; cur = analyticsNextBucket(cur, granularity) {
		k := cur.Format("2006-01-02")
		v := by[k]
		out = append(out, analyticsTimeseriesPoint{Date: k, Day: analyticsBucketLabel(cur, granularity), Clicks: v.clicks, Unique: v.unique})
	}
	return out, nil
}

func loadAnalyticsGeo(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter) (analyticsGeoResponse, error) {
	out := analyticsGeoResponse{Countries: []analyticsCountryStat{}}
	w, a := filter.eventWhere(uid, &filter.Start, &filter.End)
	rows, err := tx.QueryContext(ctx, `SELECT c.country_code,COUNT(*),COUNT(DISTINCT NULLIF(c.visitor_hash,'')) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+w+` AND c.country_code<>'' GROUP BY c.country_code ORDER BY COUNT(*) DESC,c.country_code ASC`, a...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var it analyticsCountryStat
		if err := rows.Scan(&it.Code, &it.Clicks, &it.Unique); err != nil {
			rows.Close()
			return out, err
		}
		out.Countries = append(out.Countries, it)
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(CASE WHEN c.country_code<>'' THEN 1 END),COUNT(*) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+w, a...).Scan(&out.LocatedClicks, &out.TotalClicks); err != nil {
		return out, err
	}
	if out.TotalClicks > 0 {
		out.Coverage = round1(float64(out.LocatedClicks) / float64(out.TotalClicks) * 100)
	}
	return out, nil
}

func loadAnalyticsGrouped(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter, dimension string) ([]analyticsGroupedStat, error) {
	expr := ""
	switch dimension {
	case "device":
		expr = "COALESCE(NULLIF(c.device,''),'Unknown')"
	case "browser":
		expr = "COALESCE(NULLIF(c.browser,''),'Unknown')"
	default:
		return nil, errors.New("unsupported analytics dimension")
	}
	w, a := filter.eventWhere(uid, &filter.Start, &filter.End)
	args := append(a, analyticsMaxFacetItems)
	rows, err := tx.QueryContext(ctx, `SELECT `+expr+`,COUNT(*),COUNT(DISTINCT NULLIF(c.visitor_hash,'')) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+w+` GROUP BY 1 ORDER BY 2 DESC,1 ASC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analyticsGroupedStat{}
	var total int64
	for rows.Next() {
		var it analyticsGroupedStat
		if err := rows.Scan(&it.Name, &it.Clicks, &it.Unique); err != nil {
			return nil, err
		}
		out = append(out, it)
		total += it.Clicks
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if total > 0 {
			out[i].Value = round1(float64(out[i].Clicks) / float64(total) * 100)
		}
	}
	return out, nil
}
func loadAnalyticsReferrers(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter) ([]analyticsReferrerStat, error) {
	w, a := filter.eventWhere(uid, &filter.Start, &filter.End)
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(NULLIF(c.referrer,''),'Direct'),COUNT(*),COUNT(DISTINCT NULLIF(c.visitor_hash,'')) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+w+` GROUP BY 1 ORDER BY 2 DESC,1 ASC LIMIT 20`, a...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analyticsReferrerStat{}
	for rows.Next() {
		var it analyticsReferrerStat
		if err := rows.Scan(&it.Source, &it.Clicks, &it.Unique); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func loadAnalyticsTopLinks(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter) ([]analyticsTopLinkStat, error) {
	var query string
	var args []any
	if filter.usesEventDimensions() {
		w, a := filter.eventWhere(uid, &filter.Start, &filter.End)
		query = `SELECT l.id,l.short_code,l.domain,l.redirect_type,l.destination_url,COUNT(*),COUNT(DISTINCT NULLIF(c.visitor_hash,'')),l.click_count FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE ` + w + ` GROUP BY l.id ORDER BY COUNT(*) DESC,l.id ASC LIMIT 10`
		args = a
	} else {
		w, a := filter.rollupWhere(uid, &filter.Start, &filter.End)
		query = `WITH top_links AS (SELECT l.id,l.short_code,l.domain,l.redirect_type,l.destination_url,COALESCE(SUM(cr.click_count),0) clicks,l.click_count all_time_clicks FROM click_rollups cr JOIN links l ON l.id=cr.link_id WHERE ` + w + ` GROUP BY l.id ORDER BY clicks DESC,l.id ASC LIMIT 10), unique_by_link AS (SELECT c.link_id,COUNT(DISTINCT NULLIF(c.visitor_hash,'')) unique_visitors FROM analytics_events c JOIN top_links tl ON tl.id=c.link_id WHERE c.occurred_at>=? AND c.occurred_at<? GROUP BY c.link_id) SELECT tl.id,tl.short_code,tl.domain,tl.redirect_type,tl.destination_url,tl.clicks,COALESCE(ub.unique_visitors,0),tl.all_time_clicks FROM top_links tl LEFT JOIN unique_by_link ub ON ub.link_id=tl.id ORDER BY tl.clicks DESC,tl.id ASC`
		args = append(a, filter.Start, filter.End)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analyticsTopLinkStat{}
	for rows.Next() {
		var it analyticsTopLinkStat
		if err := rows.Scan(&it.ID, &it.ShortCode, &it.Domain, &it.RedirectType, &it.DestinationURL, &it.Clicks, &it.Unique, &it.AllTimeClicks); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func loadAnalyticsHours(ctx context.Context, tx *sql.Tx, uid int64, filter analyticsFilter) ([]analyticsHourlyStat, error) {
	by := map[int]int64{}
	var rows *sql.Rows
	var err error
	if filter.usesEventDimensions() {
		w, a := filter.eventWhere(uid, &filter.Start, &filter.End)
		rows, err = tx.QueryContext(ctx, `SELECT CAST(strftime('%H',c.occurred_at) AS INTEGER),COUNT(*) FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE `+w+` GROUP BY 1 ORDER BY 1`, a...)
	} else {
		w, a := filter.rollupWhere(uid, &filter.Start, &filter.End)
		rows, err = tx.QueryContext(ctx, `SELECT CAST(strftime('%H',cr.bucket_hour) AS INTEGER),COALESCE(SUM(cr.click_count),0) FROM click_rollups cr JOIN links l ON l.id=cr.link_id WHERE `+w+` GROUP BY 1 ORDER BY 1`, a...)
	}
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h int
		var c int64
		if err := rows.Scan(&h, &c); err != nil {
			rows.Close()
			return nil, err
		}
		if h >= 0 && h < 24 {
			by[h] = c
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]analyticsHourlyStat, 24)
	for h := 0; h < 24; h++ {
		out[h] = analyticsHourlyStat{Hour: h, Clicks: by[h]}
	}
	return out, nil
}

func loadAnalyticsOptions(ctx context.Context, tx *sql.Tx, uid int64) (analyticsOptions, error) {
	out := analyticsOptions{Links: []analyticsLinkOption{}, Countries: []string{}, Regions: []analyticsRegionOption{}, Devices: []string{}, Browsers: []string{}, Referrers: []string{}}
	rows, err := tx.QueryContext(ctx, `SELECT id,short_code,domain FROM links WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, uid, analyticsMaxLinkOptions)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var it analyticsLinkOption
		if err := rows.Scan(&it.ID, &it.ShortCode, &it.Domain); err != nil {
			rows.Close()
			return out, err
		}
		out.Links = append(out.Links, it)
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	if out.Countries, err = loadDistinctAnalyticsValues(ctx, tx, `SELECT DISTINCT c.country_code FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE l.user_id=? AND c.country_code<>'' ORDER BY 1 LIMIT ?`, uid); err != nil {
		return out, err
	}
	if out.Devices, err = loadDistinctAnalyticsValues(ctx, tx, `SELECT DISTINCT COALESCE(NULLIF(c.device,''),'Unknown') FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE l.user_id=? ORDER BY 1 LIMIT ?`, uid); err != nil {
		return out, err
	}
	if out.Browsers, err = loadDistinctAnalyticsValues(ctx, tx, `SELECT DISTINCT COALESCE(NULLIF(c.browser,''),'Unknown') FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE l.user_id=? ORDER BY 1 LIMIT ?`, uid); err != nil {
		return out, err
	}
	out.Referrers, err = loadDistinctAnalyticsValues(ctx, tx, `SELECT DISTINCT COALESCE(NULLIF(c.referrer,''),'Direct') FROM analytics_events c JOIN links l ON l.id=c.link_id WHERE l.user_id=? ORDER BY 1 LIMIT ?`, uid)
	return out, err
}
func loadDistinctAnalyticsValues(ctx context.Context, tx *sql.Tx, query string, uid int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, uid, analyticsMaxFacetItems)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func parseAnalyticsTimestamp(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
		if p, err := time.Parse(layout, value); err == nil {
			return p, true
		}
	}
	return time.Time{}, false
}
func analyticsGranularity(start, end time.Time) string {
	days := analyticsCalendarDays(start, end)
	if days <= 120 {
		return "day"
	}
	if days <= 730 {
		return "week"
	}
	return "month"
}
func analyticsBucketExpression(column, granularity string) string {
	switch granularity {
	case "week":
		return `date(` + column + `,'-' || ((CAST(strftime('%w',` + column + `) AS INTEGER)+6)%7) || ' days')`
	case "month":
		return `strftime('%Y-%m-01',` + column + `)`
	default:
		return `date(` + column + `)`
	}
}
func analyticsBucketStart(value time.Time, granularity string) time.Time {
	value = value.UTC()
	base := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	switch granularity {
	case "week":
		return base.AddDate(0, 0, -((int(base.Weekday()) + 6) % 7))
	case "month":
		return time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return base
	}
}
func analyticsNextBucket(value time.Time, granularity string) time.Time {
	switch granularity {
	case "week":
		return value.AddDate(0, 0, 7)
	case "month":
		return value.AddDate(0, 1, 0)
	default:
		return value.AddDate(0, 0, 1)
	}
}
func analyticsBucketLabel(value time.Time, granularity string) string {
	if granularity == "month" {
		return value.Format("Jan 2006")
	}
	return value.Format("Jan 2")
}
func analyticsCalendarDays(start, end time.Time) int {
	sd := time.Date(start.UTC().Year(), start.UTC().Month(), start.UTC().Day(), 0, 0, 0, 0, time.UTC)
	ed := time.Date(end.UTC().Year(), end.UTC().Month(), end.UTC().Day(), 0, 0, 0, 0, time.UTC)
	d := int(ed.Sub(sd).Hours()/24) + 1
	if d < 1 {
		return 1
	}
	return d
}
