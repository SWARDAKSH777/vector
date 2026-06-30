# Changelog

## 6.0.0-rc8 — purge verification and release-warning correction

- Fixed the total-purge script silently ignoring `userdel` and `groupdel`
  failures.
- The purge now terminates remaining Vector-owned processes, verifies that no
  such processes remain, removes the system user and group, and fails if
  either identity survives.
- Corrected the installer warning so release candidates are described as
  pre-release builds rather than incorrectly described as unsigned builds.
- GitHub artifact signing/provenance remains externally verifiable using
  `gh attestation verify`.


## 6.0.0-rc5 — bootstrap fail-closed correction (independently verified)

This entry documents fixes applied after an independent build-and-test pass
that actually compiled the source and ran the test suite, rather than relying
on a prior self-reported audit document alone.

- Fixed four bootstrap-flow call sites (`bootstrapAuthenticated`,
  `handleBootstrapLogin`, `requireBootstrap`, `bootstrapStatus`) plus the CSRF
  token-source selector (`csrfSource`) that still used the fail-silent
  `getConfig` helper instead of the fail-closed `getConfigE` helper. Under a
  database outage, these previously read `setup_complete` as an empty string
  and treated that as "setup is not complete," which kept the bootstrap login
  surface reachable.
- `bootstrapAuthenticated` now returns `(authenticated, ok bool)`; callers
  must check `ok` and fail closed with `503` when the configuration value
  could not be determined.
- Added `TestBootstrapFailsClosedOnConfigOutage`, which closes the only
  database connection before any config cache is warmed and asserts that
  `requireBootstrap`, `handleBootstrapLogin`, and `bootstrapStatus` all return
  503/unavailable rather than treating the outage as "setup not complete."
  This test was confirmed to fail against the pre-fix code: under simulated
  outage, `handleBootstrapLogin` returned `200 OK` and issued a valid
  bootstrap session, because bootstrap credential verification reads a local
  file and does not itself depend on the database connection.
- Verified independently: `go build`, `go vet`, `go test ./...`, and
  `go test -race` (main package and `sqlite3local`) all pass after this fix.
  `npm ci --include=optional`, `tsc --noEmit`, and `vite build` were also
  reproduced from a clean install and match the previously reported results.

## 6.0.0-rc5 — Cloudflare country-source and analytics UI correction

- Replaced proxy-edge country lookups with verified `CF-IPCountry` and `CF-Connecting-IP` handling.
- Added an Nginx trust map for Cloudflare's official IPv4/IPv6 ranges; direct clients cannot spoof Cloudflare headers.
- Added a weekly fail-closed range updater with CIDR validation, atomic replacement, `nginx -t`, rollback, and reload.
- Kept encrypted IPinfo Lite as an optional fallback.
- Added a one-time migration that clears only stale countries and GeoIP cache while preserving every other analytics field and counter.
- Replaced the broken multi-row peak-hours grid with one consistent 24-hour chart.
- Rebuilt the responsive map with logarithmic intensity, keyboard/touch selection, tooltips, small-country markers, mobile chips, and a separate legend.

## 6.0.0-rc5 — analytics ground-zero rebuild

- Replaced split analytics writes with transactional click/event ingestion.
- Added the privacy-preserving `analytics_events` schema and safe legacy migration.
- Rebuilt browser, device, and operating-system classification.
- Added observable analytics capture health.
- Replaced the Analytics backend with one consistent report transaction.
- Rebuilt the Analytics page from the new report contract while preserving all existing panels and filters.
- Updated Dashboard to use the same report engine.
- Added route/data prefetch, bounded request deduplication, short private memory caches, and immutable hashed assets.
- Added end-to-end browser/device/unique-visitor regressions and a one-connection SQLite test.

# User analytics merge update — 2026-06-26

- Merged the submitted analytics backend, Analytics page, and Dashboard page changes.
- Fixed the empty peak-hours state so an all-zero dataset no longer reports a false `00:00` peak or performs a zero-denominator height calculation.
- Hide unique-visitor chart series when detailed analytics is disabled and show `N/A` instead of a misleading zero for unique visitors, repeat rate, and geo coverage where appropriate.
- Replaced the top-links per-row unique-visitor lookup with one bounded SQLite CTE that first selects the authenticated user's top ten links, then calculates range-scoped unique visitors only for those links.
- Added regression coverage for duplicate visitor hashes, blank hashes, range boundaries, ordering, and all-time counters.

# Security hardening update — 2026-06-26

- Added authenticated Nginx-to-Vector proxy headers to prevent local forwarded-header spoofing.
- Added explicit single-admin authorization and disabled unexpected legacy accounts.
- Hardened the privileged helper, Nginx ownership checks, command environment, symlink handling, and concurrency.
- Made installer integrity checks fail closed before root executes bundled code.
- Added account-wide login throttling, bounded caches/rate limiters, direct-admin-surface restrictions, session caps, and hardened external API clients.
- Added comprehensive security regression tests and a preserving rollback-capable upgrade.

# Changelog

## v6.0.0-rc5 helper/IPinfo settings hotfix

- Fixed privileged-helper access by moving the socket from `/run/vector/helper.sock` to `/run/vector-helper.sock`, avoiding an untraversable `root:root` runtime directory.
- Added write-only IPinfo Lite token management to Settings with provider validation, authenticated/CSRF-protected endpoints, AES-GCM encryption, replacement, and deletion.
- Removed terminal-based IPinfo token setup from the installer and documentation; legacy environment tokens are migrated once into encrypted database storage and then removed from `runtime.env`.
- Made the country resolver accept token changes immediately without restarting Vector.

## 6.0.0-rc5 — domain-scoped case-sensitive slugs, low-memory runtime, and geographic analytics

- Changed link identity from a global short code to `(domain, redirect type, exact-case slug)`, allowing the same slug on different domains and `Test`/`test` on one domain. Domains remain normalized and case-insensitive.
- Added an idempotent SQLite migration that preserves link IDs and analytics while replacing the legacy global unique constraint with a domain-scoped binary-collation index.
- Generate an unbiased cryptographically random seven-character alphanumeric slug or subdomain prefix whenever the alias is blank.
- Prevent deletion of the current default domain in both the backend and responsive domain UI; another active domain must first be selected as default.
- Scoped alias availability checks and temporary reservations to the selected domain, redirect type, and exact slug case.
- Added a low-memory profile with smaller SQLite/GeoIP/HTTP pools, bounded caches and queues, lazy limiter cleanup, `GOMAXPROCS=1`, `GOMEMLIMIT=64MiB`, and `GOGC=50`.
- Converted the privileged helper to systemd socket activation with a 30-second idle exit, removing the helper process from steady-state RAM.
- Added regressions for cross-domain duplicate slugs, same-domain case variants, automatic aliases, default-domain deletion protection, and lowercase generated subdomain prefixes.
- Unified the Links page and Analytics page around a resettable visible click counter while preserving a separate lifetime counter for irreversible max-click enforcement.
- Added anonymous hourly click rollups so totals and timeseries remain accurate when detailed analytics are disabled or visitors assert Global Privacy Control / Do Not Track; rollups remain until explicit analytics/link deletion rather than being silently removed with detailed-event retention.
- Added a one-time, idempotent migration that backfills rollups from retained historical click events without inventing timestamps for deleted or opted-out data.
- Made **Delete analytics** remove detailed events and aggregate rollups and reset the click values shown on Links, while preserving the hidden lifetime counter used by click limits.
- Added country-only geolocation through IPinfo Lite with a bounded asynchronous worker queue, short timeouts, limited retries, a circuit breaker, HMAC-keyed persistent caching, IPv4/IPv6 handling, and no raw-IP persistence. Redirects never wait for the provider.
- Made `CF-Connecting-IP` opt-in through `TRUST_CLOUDFLARE_HEADERS` and documented the required Cloudflare-only origin firewall boundary; direct Nginx deployments use the trusted proxy path by default.
- Added a responsive interactive world map used as the country filter, plus link/device/browser filters, top countries, top links, peak hours, repeat rate, geographic coverage, referrer breakdowns, and range comparison.
- Excluded HEAD requests, browser prefetch/prerender traffic, and common social/search preview crawlers from click counters.
- Added responsive mobile layouts and kept the analytics bundle lazy-loaded so the map libraries do not increase initial dashboard load.
- Added regression coverage for counter consistency, retained-history migration, analytics deletion, max-click preservation, privacy opt-outs, asynchronous country resolution/cache behavior, trusted-proxy handling, and preview-bot exclusion.

## 6.0.0-rc4 — privileged-helper protocol fix

- Fixed a Unix-socket protocol deadlock where the web process waited for the helper response while the helper waited for request EOF. The client now half-closes its write direction after sending exactly one JSON request.
- Increased the helper round-trip deadline to 15 minutes while keeping Certbot bounded to 12 minutes.
- Added explicit helper authorization, panic, and response-write logging instead of silent EOF failures.
- Added a regression test that fails if the helper protocol waits for the socket deadline.

## 6.0.0-rc3 — Bootstrap/CSRF session selection fix

- Fixed initial setup failing with `authentication required` when a browser retained a stale `vector_setup_session` or `__Host-vector_session` cookie alongside a valid bootstrap cookie.
- CSRF token issuance now selects a cryptographically valid authentication source instead of the first cookie merely present.
- During incomplete setup, a valid installer bootstrap session takes precedence over obsolete administrator cookies.
- Administrator authentication now skips stale session cookies and continues to a newer valid session cookie.
- Added regression tests for stale setup and secure cookies shadowing valid bootstrap/administrator sessions.
- Increased the temporary setup proxy timeout to 12 minutes for DNS-01 issuance and disabled response buffering.
- Fixed fresh installs failing to expose the temporary setup listener when Nginx was already active by explicitly reloading it.
- Added a backend-only official build target for audited embedded frontend assets.
- Extended total purge to remove Vector backup services/archives and all firewalld TCP 8080 rich rules.
- Added a destructive ground-zero workflow that verifies source, builds with Go 1.26.4, purges old state, removes old release artifacts, and installs from a new checksum manifest.
- Added a root-managed `VECTOR_PUBLIC_IP` fallback so automatic primary DNS creation works through SSH tunnels or private-interface VPS setups without trusting client-forwarded headers.

## 6.0.0-rc2 — setup DNS and long-running provisioning fixes

- Removed the unnecessary CSRF-token round trip from the read-only bootstrap domain check while retaining same-origin and bootstrap-session enforcement.
- Preserved real setup API error messages instead of reporting every failure as a port-80/DNS problem.
- Allowed a valid scoped Cloudflare token to validate a fresh hostname before its DNS record exists.
- Added safe automatic creation of the primary proxied A/AAAA record from the public IP used to access the protected bootstrap listener.
- Extended only the nginx/SSL setup response deadline to 12 minutes so DNS-01 propagation does not hit the global 45-second HTTP write timeout.

## 6.0.0-rc1 — security and production hardening

- Replaced broad passwordless sudo and writable system paths with a constrained root helper over a Unix socket.
- Added external master-key management and versioned AES-256-GCM encryption for Cloudflare tokens.
- Added server-side revocable sessions, secure cookie rotation, CSRF protection, request body limits, strict JSON decoding, rate limiting, stronger security headers, and HTTPS-only administrator login after setup.
- Added a one-time bootstrap gate for initial setup with PBKDF2-protected installer credentials.
- Added exact DNS ownership tracking and transactional subdomain link/DNS creation and deletion.
- Added safe destination validation to both create and update paths.
- Replaced password-verifier cookies with opaque signed unlock grants and added brute-force throttling.
- Added privacy-preserving analytics, GPC/DNT handling, configurable retention, audit logs, data export, and analytics deletion.
- Added persistent root-only Certbot DNS credentials, renewal timer integration, and a safe Nginx reload deploy hook.
- Added default domains and a styled domain selector.
- Added warning-only release manifest verification, SBOM, security/compliance documentation, and hardened install/purge scripts.
- Removed the obsolete HTTP ownership challenge and legacy sudo/Nginx include paths.
- Split frontend bundles with lazy loading and removed external font dependencies.
- Restricted the helper socket to the exact `vector` Unix UID and configured loopback backend port.
- Added certificate ownership markers so Vector cannot adopt or delete unrelated certificates.
- Bound redirect/unlock behavior to the selected host and blocked dashboard/setup exposure on wildcard and non-default domains.
- Prevented deletion of the primary origin or final active domain and made the IP redirect follow the selected default domain.
- Added SQLite integrity checks, secure deletion, uniqueness normalization, and optimized analytics aggregation.
- Pinned official release builds to Go 1.26.4 and made the Makefile reject stale release toolchains.

## 5.0.0

- Added installer-generated setup bootstrap credentials and protected all setup APIs.

## 4.0.0

- Added Cloudflare-first domain provisioning, DNS-before-link transactions, default domains, and a styled domain selector.

## Earlier fixes

- Accepted any real HTTP response during no-token reachability checks, including default Nginx 404 responses.
- Added Nginx validation/rollback and surfaced Certbot errors to the setup UI.
