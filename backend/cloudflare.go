package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var cloudflareAPIBaseURL = "https://api.cloudflare.com/client/v4"

var cloudflareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type cloudflareClient struct {
	apiToken        string
	targetHost      string
	primaryOriginIP string
	http            *http.Client
}

// cfDNSCache caches DNS records per zone to avoid hammering the CF API.
type cfDNSCache struct {
	mu      sync.RWMutex
	records map[string][]cfDNSRecord // token digest + zoneID -> records
	expires map[string]time.Time
}

var dnsCache = &cfDNSCache{
	records: make(map[string][]cfDNSRecord),
	expires: make(map[string]time.Time),
}

func defaultHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       45 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfResponse[T any] struct {
	Success bool            `json:"success"`
	Errors  []cfResponseErr `json:"errors"`
	Result  T               `json:"result"`
}

type cfResponseErr struct {
	Message string `json:"message"`
}

type cfHTTPError struct {
	Status  int
	Message string
}

func (e *cfHTTPError) Error() string {
	return fmt.Sprintf("Cloudflare API returned HTTP %d: %s", e.Status, e.Message)
}

type cfDNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

func (c *cloudflareClient) request(method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	base := strings.TrimRight(cloudflareAPIBaseURL, "/")
	req, err := http.NewRequest(method, base+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare API unreachable: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("could not read Cloudflare API response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Errors []cfResponseErr `json:"errors"`
		}
		_ = json.Unmarshal(data, &apiErr)
		message := sanitizeCloudflareMessage(firstErr(apiErr.Errors))
		if message == "unknown error" {
			message = sanitizeCloudflareMessage(string(data))
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return &cfHTTPError{Status: resp.StatusCode, Message: message}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("Cloudflare API returned HTTP %d with an invalid response: %v", resp.StatusCode, err)
	}
	return nil
}

func (c *cloudflareClient) findZoneForHostname(hostname string) (*cfZone, error) {
	var best *cfZone
	for page := 1; page <= 20; page++ {
		query := url.Values{"per_page": {"50"}, "status": {"active"}, "page": {strconv.Itoa(page)}}
		var res cfResponse[[]cfZone]
		if err := c.request("GET", "/zones?"+query.Encode(), nil, &res); err != nil {
			return nil, err
		}
		if !res.Success {
			return nil, fmt.Errorf("Cloudflare API error: %s — check your token has Zone:Read permission", firstErr(res.Errors))
		}
		for i := range res.Result {
			z := res.Result[i]
			if hostname == z.Name || strings.HasSuffix(hostname, "."+z.Name) {
				if best == nil || len(z.Name) > len(best.Name) {
					copy := z
					best = &copy
				}
			}
		}
		if len(res.Result) < 50 {
			break
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no active Cloudflare zone found for %q — add the domain to Cloudflare first or expand the token's zone scope", hostname)
	}
	return best, nil
}

func (c *cloudflareClient) dnsCacheKey(zoneID string) string {
	// Scope cached authorization-sensitive results to the API token. Otherwise
	// replacing a revoked token could reuse records fetched by the old token and
	// falsely pass validation until the cache expires.
	return tokenDigest(c.apiToken) + "\x00" + zoneID
}

// listZoneRecords returns all DNS records for a zone, using a short cache.
func (c *cloudflareClient) listZoneRecords(zoneID string) ([]cfDNSRecord, error) {
	if !cloudflareIDPattern.MatchString(zoneID) {
		return nil, errors.New("invalid Cloudflare zone identifier")
	}
	cacheKey := c.dnsCacheKey(zoneID)
	dnsCache.mu.RLock()
	if recs, ok := dnsCache.records[cacheKey]; ok && time.Now().Before(dnsCache.expires[cacheKey]) {
		dnsCache.mu.RUnlock()
		return append([]cfDNSRecord(nil), recs...), nil
	}
	dnsCache.mu.RUnlock()

	var all []cfDNSRecord
	for page := 1; page <= 100; page++ {
		query := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var res cfResponse[[]cfDNSRecord]
		if err := c.request("GET", "/zones/"+zoneID+"/dns_records?"+query.Encode(), nil, &res); err != nil {
			return nil, err
		}
		if !res.Success {
			return nil, fmt.Errorf("could not list DNS records: %s", firstErr(res.Errors))
		}
		all = append(all, res.Result...)
		if len(res.Result) < 100 {
			break
		}
	}
	dnsCache.mu.Lock()
	now := time.Now()
	for key, expiry := range dnsCache.expires {
		if now.After(expiry) {
			delete(dnsCache.expires, key)
			delete(dnsCache.records, key)
		}
	}
	if len(dnsCache.records) >= 128 {
		for key := range dnsCache.records {
			delete(dnsCache.records, key)
			delete(dnsCache.expires, key)
			break
		}
	}
	dnsCache.records[cacheKey] = append([]cfDNSRecord(nil), all...)
	dnsCache.expires[cacheKey] = now.Add(30 * time.Second)
	dnsCache.mu.Unlock()
	return all, nil
}

// invalidateCache clears the DNS record cache for a zone after mutations.
func invalidateCache(zoneID string) {
	dnsCache.mu.Lock()
	suffix := "\x00" + zoneID
	for key := range dnsCache.records {
		if strings.HasSuffix(key, suffix) {
			delete(dnsCache.records, key)
			delete(dnsCache.expires, key)
		}
	}
	dnsCache.mu.Unlock()
}

// CheckSubdomainConflict checks if a hostname already has an A, AAAA or CNAME record.
// Returns a human-readable message if there's a conflict, empty string if clear.
func (c *cloudflareClient) CheckSubdomainConflict(zoneID, hostname string) (string, error) {
	recs, err := c.listZoneRecords(zoneID)
	if err != nil {
		return "", err
	}
	for _, r := range recs {
		if strings.EqualFold(r.Name, hostname) && (r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME") {
			return fmt.Sprintf("%s record already exists for %q (points to %s)", r.Type, hostname, r.Content), nil
		}
	}
	return "", nil
}

// ensureRecord creates a CNAME when the base domain has no existing address
// record. Existing A/AAAA/CNAME records are preserved; Vector must never
// silently replace a domain that may already serve another application.
func (c *cloudflareClient) ensureRecord(zoneID, hostname string) (*cfDNSRecord, bool, error) {
	if c.targetHost == "" {
		return nil, false, fmt.Errorf("server hostname not configured — complete setup first")
	}

	recs, err := c.listZoneRecords(zoneID)
	if err != nil {
		return nil, false, err
	}
	for i := range recs {
		r := recs[i]
		if !strings.EqualFold(r.Name, hostname) || (r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME") {
			continue
		}
		if strings.EqualFold(hostname, c.targetHost) {
			// During initial setup the bootstrap request tells us the public IP of
			// this server. Do not accept an arbitrary pre-existing primary record:
			// doing so would complete setup and redirect the administrator to a
			// hostname that may still serve another machine. Later verification
			// calls do not carry primaryOriginIP and therefore preserve an existing
			// record rather than trying to rewrite administrator-managed DNS.
			if rawOrigin := strings.TrimSpace(c.primaryOriginIP); rawOrigin != "" {
				expected := net.ParseIP(rawOrigin)
				if expected == nil || !isPublicDestinationIP(expected) {
					return nil, false, fmt.Errorf("the detected setup origin IP is not a public address")
				}
				if r.Type == "CNAME" {
					return nil, false, fmt.Errorf("primary domain %q already has a CNAME to %q; replace it with an A/AAAA record to this server (%s) before completing setup", hostname, r.Content, expected.String())
				}
				actual := net.ParseIP(strings.TrimSpace(r.Content))
				if actual == nil || !actual.Equal(expected) {
					return nil, false, fmt.Errorf("primary domain %q has an existing %s record pointing to %q, not this server (%s); correct DNS before completing setup", hostname, r.Type, r.Content, expected.String())
				}
			}
			return &recs[i], false, nil
		}
		if r.Type == "CNAME" && strings.EqualFold(strings.TrimSuffix(r.Content, "."), strings.TrimSuffix(c.targetHost, ".")) {
			return &recs[i], false, nil
		}

		// When the custom domain already has an A/AAAA record, compare its
		// origin value with the primary Vector domain's Cloudflare record. This
		// proves it reaches the same origin without replacing administrator DNS.
		targetZone, zoneErr := c.findZoneForHostname(c.targetHost)
		if zoneErr == nil {
			targetRecords, recordsErr := c.listZoneRecords(targetZone.ID)
			if recordsErr == nil {
				for _, target := range targetRecords {
					if strings.EqualFold(target.Name, c.targetHost) && target.Type == r.Type &&
						strings.EqualFold(strings.TrimSuffix(target.Content, "."), strings.TrimSuffix(r.Content, ".")) {
						return &recs[i], false, nil
					}
				}
			}
		}
		return nil, false, fmt.Errorf("existing %s record for %q points to %q, not Vector; change it to CNAME %q or to the same origin as %q, then verify again", r.Type, hostname, r.Content, c.targetHost, c.targetHost)
	}

	// During initial setup, Vector may safely create the primary A/AAAA record
	// from the public IP used to access the protected bootstrap listener. For
	// later domain operations, the primary record must already exist.
	if strings.EqualFold(hostname, c.targetHost) {
		ip := net.ParseIP(strings.TrimSpace(c.primaryOriginIP))
		if ip == nil || !isPublicDestinationIP(ip) {
			return nil, false, fmt.Errorf("primary domain %q has no A, AAAA, or CNAME record; create an A record to this server's public IP and retry", hostname)
		}
		recordType := "AAAA"
		if ip.To4() != nil {
			recordType = "A"
		}
		created, err := c.createRecord(zoneID, cfDNSRecord{
			Type:    recordType,
			Name:    hostname,
			Content: ip.String(),
			Proxied: true,
			TTL:     1,
		})
		return created, err == nil, err
	}

	created, err := c.createRecord(zoneID, cfDNSRecord{
		Type:    "CNAME",
		Name:    hostname,
		Content: c.targetHost,
		Proxied: true,
		TTL:     1,
	})
	return created, err == nil, err
}

func (c *cloudflareClient) createRecord(zoneID string, rec cfDNSRecord) (*cfDNSRecord, error) {
	if !cloudflareIDPattern.MatchString(zoneID) {
		return nil, errors.New("invalid Cloudflare zone identifier")
	}
	var created cfResponse[cfDNSRecord]
	if err := c.request("POST", "/zones/"+zoneID+"/dns_records", rec, &created); err != nil {
		return nil, err
	}
	if !created.Success {
		return nil, fmt.Errorf("could not create DNS record: %s", firstErr(created.Errors))
	}
	invalidateCache(zoneID)
	return &created.Result, nil
}

// createSubdomainRecord is deliberately strict: it never reuses or overwrites
// an existing hostname. Link creation must fail rather than silently hijack a
// DNS record owned by another service.
func (c *cloudflareClient) createSubdomainRecord(zoneID, hostname string) (*cfDNSRecord, error) {
	if c.targetHost == "" {
		return nil, fmt.Errorf("server hostname not configured — complete setup first")
	}
	conflict, err := c.CheckSubdomainConflict(zoneID, hostname)
	if err != nil {
		return nil, err
	}
	if conflict != "" {
		return nil, fmt.Errorf("%s", conflict)
	}
	return c.createRecord(zoneID, cfDNSRecord{
		Type:    "CNAME",
		Name:    hostname,
		Content: c.targetHost,
		Proxied: true,
		TTL:     1,
	})
}

func (c *cloudflareClient) deleteRecordByID(zoneID, recordID string) error {
	if recordID == "" {
		return nil
	}
	if !cloudflareIDPattern.MatchString(zoneID) || !cloudflareIDPattern.MatchString(recordID) {
		return errors.New("invalid Cloudflare DNS identifier")
	}
	var res cfResponse[struct{}]
	if err := c.request("DELETE", "/zones/"+zoneID+"/dns_records/"+recordID, nil, &res); err != nil {
		var httpErr *cfHTTPError
		if errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound {
			invalidateCache(zoneID)
			return nil
		}
		return err
	}
	if !res.Success {
		return fmt.Errorf("could not delete DNS record: %s", firstErr(res.Errors))
	}
	invalidateCache(zoneID)
	return nil
}

// GetSubdomainBlocklist returns all taken A/AAAA/CNAME hostnames for a zone (for the UI).
func (c *cloudflareClient) GetSubdomainBlocklist(zoneID string) ([]string, error) {
	recs, err := c.listZoneRecords(zoneID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var blocked []string
	for _, r := range recs {
		if (r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME") && !seen[r.Name] {
			seen[r.Name] = true
			blocked = append(blocked, r.Name)
		}
	}
	return blocked, nil
}

func firstErr(errs []cfResponseErr) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	return errs[0].Message
}

func sanitizeCloudflareMessage(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			b.WriteRune(r)
		}
		if b.Len() >= 1024 {
			break
		}
	}
	if b.Len() == 0 {
		return "unknown error"
	}
	return b.String()
}
