package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	ipinfoLiteEndpoint      = "https://api.ipinfo.io/lite"
	defaultGeoWorkers       = 1
	defaultGeoQueueSize     = 256
	defaultGeoMemoryEntries = 512
	maxPendingClickIDs      = 2048
	defaultGeoCacheTTL      = 7 * 24 * time.Hour
	defaultGeoNegativeTTL   = time.Hour
	geoLookupRequestTimeout = 4 * time.Second
)

var errGeoCircuitOpen = errors.New("country lookup circuit is temporarily open")

type geoLookupJob struct {
	IP         string
	IPHash     string
	Generation uint64
}

type pendingCountryLookup struct {
	Generation uint64
	ClickIDs   []int64
}

type cachedCountry struct {
	Code      string
	ExpiresAt time.Time
}

type countryGeoResolver struct {
	db            *sql.DB
	masterKey     []byte
	tokenMu       sync.RWMutex
	token         string
	endpoint      string
	client        *http.Client
	cacheTTL      time.Duration
	negativeTTL   time.Duration
	memoryEntries int

	queue chan geoLookupJob
	stop  chan struct{}
	wg    sync.WaitGroup

	mu             sync.Mutex
	pending        map[string]pendingCountryLookup
	memory         map[string]cachedCountry
	lookupCtx      context.Context
	lookupCancel   context.CancelFunc
	generation     uint64
	failureCount   int
	circuitUntil   time.Time
	lastFailureLog time.Time
	closed         bool
}

type countryGeoResolverConfig struct {
	Token         string
	Endpoint      string
	Client        *http.Client
	Workers       int
	QueueSize     int
	CacheTTL      time.Duration
	NegativeTTL   time.Duration
	MemoryEntries int
}

func newCountryGeoResolver(db *sql.DB, masterKey []byte) *countryGeoResolver {
	token, err := loadOrMigrateIPInfoToken(db, masterKey)
	if err != nil {
		log.Printf("IPinfo Lite token load warning: %v", err)
	}
	cfg := countryGeoResolverConfig{
		Token:         token,
		Endpoint:      ipinfoLiteEndpoint,
		Workers:       intEnvBounded("GEO_LOOKUP_WORKERS", defaultGeoWorkers, 1, 8),
		QueueSize:     intEnvBounded("GEO_LOOKUP_QUEUE_SIZE", defaultGeoQueueSize, 64, 16384),
		CacheTTL:      time.Duration(intEnvBounded("GEO_CACHE_TTL_HOURS", int(defaultGeoCacheTTL/time.Hour), 1, 24*30)) * time.Hour,
		NegativeTTL:   defaultGeoNegativeTTL,
		MemoryEntries: intEnvBounded("GEO_MEMORY_CACHE_ENTRIES", defaultGeoMemoryEntries, 0, 16384),
	}
	return newCountryGeoResolverWithConfig(db, masterKey, cfg)
}

func newCountryGeoResolverWithConfig(db *sql.DB, masterKey []byte, cfg countryGeoResolverConfig) *countryGeoResolver {
	if cfg.Endpoint == "" {
		cfg.Endpoint = ipinfoLiteEndpoint
	}
	if cfg.Client == nil {
		cfg.Client = newCountryGeoHTTPClient()
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultGeoWorkers
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultGeoQueueSize
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultGeoCacheTTL
	}
	if cfg.NegativeTTL <= 0 {
		cfg.NegativeTTL = defaultGeoNegativeTTL
	}
	if cfg.MemoryEntries < 0 {
		cfg.MemoryEntries = defaultGeoMemoryEntries
	}
	lookupCtx, lookupCancel := context.WithCancel(context.Background())
	r := &countryGeoResolver{
		db:            db,
		masterKey:     masterKey,
		token:         strings.TrimSpace(cfg.Token),
		endpoint:      strings.TrimRight(cfg.Endpoint, "/"),
		client:        cfg.Client,
		cacheTTL:      cfg.CacheTTL,
		negativeTTL:   cfg.NegativeTTL,
		memoryEntries: cfg.MemoryEntries,
		queue:         make(chan geoLookupJob, cfg.QueueSize),
		stop:          make(chan struct{}),
		pending:       make(map[string]pendingCountryLookup),
		memory:        make(map[string]cachedCountry),
		lookupCtx:     lookupCtx,
		lookupCancel:  lookupCancel,
	}
	for i := 0; i < cfg.Workers; i++ {
		r.wg.Add(1)
		go r.worker()
	}
	if r.configured() {
		log.Printf("IPinfo Lite country lookup enabled with %d worker(s), queue=%d, cache=%s", cfg.Workers, cfg.QueueSize, cfg.CacheTTL)
	} else {
		log.Printf("IPinfo Lite country lookup is not configured; add a token in Settings")
	}
	return r
}

func newCountryGeoHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   1,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 3 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   geoLookupRequestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (r *countryGeoResolver) configured() bool {
	return r != nil && validGeoToken(r.tokenSnapshot())
}

func (r *countryGeoResolver) tokenSnapshot() string {
	if r == nil {
		return ""
	}
	r.tokenMu.RLock()
	defer r.tokenMu.RUnlock()
	return r.token
}

func (r *countryGeoResolver) setToken(token string) {
	if r == nil {
		return
	}
	r.tokenMu.Lock()
	r.token = strings.TrimSpace(token)
	r.tokenMu.Unlock()
	r.reset()
	if transport, ok := r.client.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

func validGeoToken(token string) bool {
	if token == "" || len(token) > 512 {
		return false
	}
	for _, ch := range token {
		if ch < 0x21 || ch > 0x7e || unicode.IsSpace(ch) || unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func (r *countryGeoResolver) cachedCountryForIP(rawIP string) (string, bool) {
	if !r.configured() {
		return "", false
	}
	ip, ok := publicGeoIP(rawIP)
	if !ok {
		return "", true
	}
	key := stableValueHash(r.masterKey, "geo-country:"+ip)
	now := time.Now().UTC()

	r.mu.Lock()
	if item, found := r.memory[key]; found {
		if now.Before(item.ExpiresAt) {
			r.mu.Unlock()
			return item.Code, true
		}
		delete(r.memory, key)
	}
	r.mu.Unlock()

	var code string
	var expires time.Time
	err := r.db.QueryRow(`SELECT country_code,expires_at FROM geo_country_cache WHERE ip_hash=? AND expires_at>?`, key, now).Scan(&code, &expires)
	if err != nil {
		return "", false
	}
	if !validCountryCode(code) {
		code = ""
	}
	r.remember(key, code, expires)
	return code, true
}

func (r *countryGeoResolver) enqueue(clickID int64, rawIP string) {
	if clickID <= 0 || !r.configured() {
		return
	}
	ip, ok := publicGeoIP(rawIP)
	if !ok {
		return
	}
	key := stableValueHash(r.masterKey, "geo-country:"+ip)
	now := time.Now().UTC()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	if item, found := r.memory[key]; found && now.Before(item.ExpiresAt) {
		code := item.Code
		r.mu.Unlock()
		if code != "" {
			_, _ = r.db.Exec(`UPDATE analytics_events SET country_code=? WHERE id=? AND country_code=''`, code, clickID)
		}
		return
	}
	generation := r.generation
	if pending, found := r.pending[key]; found && pending.Generation == generation {
		if len(pending.ClickIDs) < maxPendingClickIDs {
			pending.ClickIDs = append(pending.ClickIDs, clickID)
			r.pending[key] = pending
		}
		r.mu.Unlock()
		return
	}
	r.pending[key] = pendingCountryLookup{Generation: generation, ClickIDs: []int64{clickID}}
	r.mu.Unlock()

	select {
	case r.queue <- geoLookupJob{IP: ip, IPHash: key, Generation: generation}:
	default:
		r.mu.Lock()
		delete(r.pending, key)
		r.mu.Unlock()
		r.logFailure(errors.New("country lookup queue is full"))
	}
}

func (r *countryGeoResolver) worker() {
	defer r.wg.Done()
	for {
		select {
		case <-r.stop:
			return
		case job := <-r.queue:
			r.mu.Lock()
			active := !r.closed && job.Generation == r.generation
			lookupCtx := r.lookupCtx
			r.mu.Unlock()
			if !active {
				continue
			}
			country, err := r.lookupCountry(lookupCtx, job.IP)
			r.mu.Lock()
			active = !r.closed && job.Generation == r.generation
			r.mu.Unlock()
			if !active {
				continue
			}
			if err != nil {
				r.recordFailure(err)
				r.finish(job, "", false)
				continue
			}
			r.recordSuccess()
			ttl := r.cacheTTL
			if country == "" {
				ttl = r.negativeTTL
			}
			expires := time.Now().UTC().Add(ttl)
			if _, err := r.db.Exec(`INSERT INTO geo_country_cache(ip_hash,country_code,expires_at,last_used_at)
				VALUES(?,?,?,CURRENT_TIMESTAMP)
				ON CONFLICT(ip_hash) DO UPDATE SET country_code=excluded.country_code,expires_at=excluded.expires_at,last_used_at=CURRENT_TIMESTAMP`,
				job.IPHash, country, expires); err != nil {
				r.logFailure(fmt.Errorf("cache country result: %w", err))
			}
			r.remember(job.IPHash, country, expires)
			r.finish(job, country, true)
		}
	}
}

func (r *countryGeoResolver) lookupCountry(parent context.Context, ip string) (string, error) {
	r.mu.Lock()
	until := r.circuitUntil
	r.mu.Unlock()
	if time.Now().Before(until) {
		return "", errGeoCircuitOpen
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(parent, geoLookupRequestTimeout)
		country, retry, err := r.doLookup(ctx, ip)
		cancel()
		if err == nil {
			return country, nil
		}
		lastErr = err
		if !retry || attempt == 1 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
		select {
		case <-parent.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return "", parent.Err()
		case <-timer.C:
		}
	}
	return "", lastErr
}

func (r *countryGeoResolver) doLookup(ctx context.Context, ip string) (string, bool, error) {
	token := r.tokenSnapshot()
	if !validGeoToken(token) {
		return "", false, errors.New("IPinfo Lite token is not configured")
	}
	return r.doLookupWithToken(ctx, ip, token)
}

func (r *countryGeoResolver) doLookupWithToken(ctx context.Context, ip, token string) (string, bool, error) {
	endpoint := r.endpoint + "/" + url.PathEscape(ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Vector/"+buildVersion)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", true, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", retry, fmt.Errorf("IPinfo Lite returned HTTP %d", resp.StatusCode)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", false, errors.New("IPinfo Lite returned a non-JSON response")
	}
	var payload struct {
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 32<<10))
	if err := dec.Decode(&payload); err != nil {
		return "", false, fmt.Errorf("decode IPinfo Lite response: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", false, errors.New("IPinfo Lite returned trailing response data")
	}
	if payload.IP != "" {
		responseIP := net.ParseIP(strings.TrimSpace(payload.IP))
		if responseIP == nil || responseIP.String() != ip {
			return "", false, errors.New("IPinfo Lite response IP did not match the request")
		}
	}
	code := strings.ToUpper(strings.TrimSpace(payload.CountryCode))
	if !validCountryCode(code) || code == "XX" || code == "T1" {
		return "", false, nil
	}
	return code, false, nil
}

func (r *countryGeoResolver) validateToken(ctx context.Context, token string) error {
	if !validGeoToken(token) {
		return errors.New("token must be a non-empty printable ASCII value without spaces")
	}
	country, _, err := r.doLookupWithToken(ctx, "1.1.1.1", token)
	if err != nil {
		return err
	}
	if country == "" {
		return errors.New("IPinfo Lite did not return a country for the validation request")
	}
	return nil
}

func (r *countryGeoResolver) finish(job geoLookupJob, country string, success bool) {
	r.mu.Lock()
	pending, found := r.pending[job.IPHash]
	if !found || pending.Generation != job.Generation {
		r.mu.Unlock()
		return
	}
	ids := append([]int64(nil), pending.ClickIDs...)
	delete(r.pending, job.IPHash)
	r.mu.Unlock()
	if !success || country == "" || len(ids) == 0 {
		return
	}
	tx, err := r.db.Begin()
	if err != nil {
		r.logFailure(fmt.Errorf("begin country update: %w", err))
		return
	}
	stmt, err := tx.Prepare(`UPDATE analytics_events SET country_code=? WHERE id=? AND country_code=''`)
	if err != nil {
		_ = tx.Rollback()
		r.logFailure(fmt.Errorf("prepare country update: %w", err))
		return
	}
	for _, id := range ids {
		if _, err := stmt.Exec(country, id); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			r.logFailure(fmt.Errorf("apply country update: %w", err))
			return
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		r.logFailure(fmt.Errorf("commit country update: %w", err))
	}
}

func (r *countryGeoResolver) remember(key, code string, expires time.Time) {
	if r.memoryEntries == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.memory[key]; !exists && len(r.memory) >= r.memoryEntries {
		now := time.Now().UTC()
		for cacheKey, item := range r.memory {
			if !now.Before(item.ExpiresAt) {
				delete(r.memory, cacheKey)
			}
		}
		for cacheKey := range r.memory {
			if len(r.memory) < r.memoryEntries {
				break
			}
			delete(r.memory, cacheKey)
		}
	}
	r.memory[key] = cachedCountry{Code: code, ExpiresAt: expires}
}

func (r *countryGeoResolver) recordFailure(err error) {
	if errors.Is(err, errGeoCircuitOpen) {
		return
	}
	r.mu.Lock()
	r.failureCount++
	if r.failureCount >= 5 {
		r.circuitUntil = time.Now().Add(time.Minute)
		r.failureCount = 0
	}
	r.mu.Unlock()
	r.logFailure(err)
}

func (r *countryGeoResolver) recordSuccess() {
	r.mu.Lock()
	r.failureCount = 0
	r.circuitUntil = time.Time{}
	r.mu.Unlock()
}

func (r *countryGeoResolver) logFailure(err error) {
	r.mu.Lock()
	if time.Since(r.lastFailureLog) < time.Minute {
		r.mu.Unlock()
		return
	}
	r.lastFailureLog = time.Now()
	r.mu.Unlock()
	log.Printf("country geolocation warning: %v", err)
}

// reset cancels in-flight lookups and removes all transient in-memory state.
// The persistent cache is deleted in the caller's database transaction.
func (r *countryGeoResolver) reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.generation++
	if r.lookupCancel != nil {
		r.lookupCancel()
	}
	r.lookupCtx, r.lookupCancel = context.WithCancel(context.Background())
	r.pending = make(map[string]pendingCountryLookup)
	r.memory = make(map[string]cachedCountry)
	r.failureCount = 0
	r.circuitUntil = time.Time{}
	for {
		select {
		case <-r.queue:
		default:
			r.mu.Unlock()
			return
		}
	}
}

func (r *countryGeoResolver) queueDepth() int {
	if r == nil {
		return 0
	}
	return len(r.queue)
}

func (r *countryGeoResolver) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.generation++
	if r.lookupCancel != nil {
		r.lookupCancel()
	}
	close(r.stop)
	r.mu.Unlock()
	r.wg.Wait()
	if transport, ok := r.client.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

func validCountryCode(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, ch := range value {
		if ch < 'A' || ch > 'Z' {
			return false
		}
	}
	return true
}

func safeGeoText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	out := make([]rune, 0, len(value))
	for _, ch := range value {
		if unicode.IsControl(ch) {
			continue
		}
		out = append(out, ch)
		if len(out) >= limit {
			break
		}
	}
	return string(out)
}

func publicGeoIP(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	ip := net.ParseIP(raw)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return "", false
	}
	for _, block := range geoDeniedNetworks {
		if block.Contains(ip) {
			return "", false
		}
	}
	return ip.String(), true
}

var geoDeniedNetworks = func() []*net.IPNet {
	values := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"192.88.99.0/24", "64:ff9b:1::/48", "100::/64", "2001:2::/48",
		"2001:10::/28", "2001:20::/28", "2001:db8::/32", "3fff::/20",
	}
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err == nil {
			out = append(out, network)
		}
	}
	return out
}()

func intEnvBounded(key string, fallback, min, max int) int {
	value, err := strconv.Atoi(strings.TrimSpace(osEnv(key)))
	if err != nil || value < min || value > max {
		return fallback
	}
	return value
}

var osEnv = func(key string) string { return getenv(key, "") }
