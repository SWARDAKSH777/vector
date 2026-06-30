# Vector Analytics Ground-Zero Rebuild

## Scope

This release replaces the analytics ingestion, event storage, reporting API,
Dashboard integration, Analytics page, and related client-side loading paths.
Link routing, domain-scoped case-sensitive slugs, domain management, encrypted
IPinfo/Cloudflare tokens, authentication, authorization, helper isolation, and
certificate management remain intact.

## Root cause of missing browser and unique-visitor data

The earlier redirect path committed the visible link counter and hourly rollup
first, then attempted a separate detailed-event insert. A busy or failed second
write could therefore leave a valid click count with no browser, device,
country, referrer, or visitor fingerprint. Those failures were not visible in
the Analytics page. The page also assembled its UI from multiple independent
requests and mixed hourly rollups with detailed events, making partial or
inconsistent states possible.

## New ingestion architecture

Every countable redirect now performs these operations in one SQLite write
transaction:

1. Increment the visible and lifetime link counters.
2. Increment the hourly rollup.
3. Insert a privacy-preserving `analytics_events` row.
4. Commit the transaction before redirecting.

The detailed event uses a savepoint. An analytics-only constraint failure cannot
break the redirect, but it is recorded in an observable capture-health status.
The normal code path commits the counter, rollup, and detailed event together,
which removes the previous split-write race.

The new event table stores:

- Link ID and UTC occurrence time
- HMAC-based anonymous visitor fingerprint
- Origin-only referrer
- Device class
- Browser family
- Operating-system family
- Two-letter country code

It does not store a raw IP address or full user-agent string. Global Privacy
Control (`Sec-GPC: 1`) and Do Not Track (`DNT: 1`) suppress the detailed event
while the aggregate click remains counted.

The anonymous fingerprint is based on the normalized client IP and coarse
client classification under Vector's external master key. It is intended for
approximate unique counts, not cross-site or permanent identity. Users behind
the same NAT with the same client classification may be merged, and changes in
network or browser may create a new anonymous visitor.

## Browser and device detection

The parser was rebuilt and tested for Chrome, Safari, Edge, Firefox, Opera,
Samsung Internet, Internet Explorer, command-line clients, desktop, mobile,
tablet, and unknown clients. Client hints are used when present, with the
user-agent as a fallback. Bot and link-preview requests remain excluded from
click counting.

## New reporting API

`GET /api/analytics/report` returns one authenticated, user-scoped report:

- Overview and previous-period comparison
- Complete aggregate click totals
- Detailed-event coverage
- Unique visitors and repeat rate
- Daily, weekly, or monthly timeline
- Country map and coverage
- Device and browser breakdowns
- Referrers
- Peak UTC hours
- Top links
- Bounded filter options
- Capture-health and GeoIP queue status

All panels are generated inside one SQLite read transaction. Filters are parsed
once, bounded, parameterized, and applied consistently. The page no longer
issues eight independent analytics queries. Legacy `/api/stats/*` routes remain
as compatibility wrappers over the same report engine.

## Existing-data migration

Legacy detailed rows are copied once into `analytics_events`. Existing browser,
device, referrer, visitor hash, timestamp, and country values are preserved.
Legacy event IDs are preserved so the privacy migration can safely synchronize
recoverable anonymous visitor hashes after raw IP removal.

Historical information that was never written cannot be reconstructed. In
particular, an old aggregate click with no corresponding detailed row has no
browser or unique-visitor evidence. New clicks after this release populate the
new event store transactionally.

## Loading-performance changes

- Authenticated route chunks are prefetched during browser idle time after login.
- Common authenticated data is prefetched into memory after login.
- Duplicate concurrent GET requests are coalesced.
- Short-lived in-memory response caching avoids immediate duplicate fetches.
- The server caches user/filter-scoped analytics reports for three seconds.
- Manual refresh bypasses both analytics caches.
- Content-hashed frontend assets use a one-year immutable cache policy.
- Configuration reads use a bounded two-second in-memory cache.
- Dashboard uses the consolidated analytics report and no longer downloads and
  sorts the full link list just to show its top five links.
- The Analytics page preserves the previous report while a new filter refreshes
  instead of replacing the page with a full-screen spinner.

These optimizations do not cache authorization decisions. Every API request
still validates the session, active administrator role, expiry, and trusted
reverse-proxy boundary on the backend. Browser caching is memory-only and is
cleared after authentication changes and relevant mutations.

The first uncached page request still depends on network latency, database size,
and server resources. After login prefetch and asset caching, navigation should
normally render from already-loaded code/data and refresh unobtrusively rather
than appearing blank.

## Security controls retained or added

- Parameterized SQL for every user-controlled filter
- Strict filter lengths and control-character rejection
- Per-user ownership condition in every analytics query
- Eight-second report timeout and bounded response dimensions
- Bounded 128-entry report cache with a three-second TTL
- No raw IP or full user-agent in the new event store
- Trusted-proxy validation before forwarded IP headers are accepted
- HMAC visitor fingerprints under the external master key
- Auth and active-admin authorization checked on every API request
- CSRF protection remains required for all state-changing APIs
- Country enrichment remains asynchronous and does not delay redirects
- Capture failures are surfaced without leaking database/provider details

## Validation performed

- Go unit and regression tests
- End-to-end redirect capture test using repeated Chrome/Windows and Safari/iOS requests
- Verification of three events, two anonymous visitors, browser/device classification, and matching rollups
- Single-SQLite-connection report test
- Go race detector
- `go vet`
- Strict TypeScript/Vite production build
- npm high-severity audit: zero known vulnerabilities
- Embedded frontend Go build
- Archive, manifest, shell-syntax, and installer checks during packaging

Local validation used the installed Go compiler because the isolated build
environment could not download the pinned toolchain. The deployment scripts
continue to download, checksum, and require Go 1.26.4 before producing the
installation binary.
