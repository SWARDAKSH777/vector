> **Current validation (2026-06-29):** the deep corrective audit is documented in `SECURITY_AUDIT_FINAL.md`. Clean frontend install/audit/build, source validation, Go tests, vet, and split race-detector runs pass. This remains source-only until the Go 1.26.4 release workflow publishes the binary, checksums, and provenance.

> **Historical status note:** use `SECURITY_AUDIT_FINAL.md` and the tag-triggered CI workflow for current release evidence. Claims below are not, by themselves, proof for a newly published binary.

# Release status

This source includes the analytics ground-zero rebuild plus the Cloudflare country-source and map/peak-hours correction described in `CLOUDFLARE_GEO_UI_REPORT.md`. Browser, device, and anonymous unique-visitor
capture now occurs transactionally with click counters. Historical aggregate
clicks that never had detailed events cannot be reconstructed.

This source tree is the domain-scoped slug, low-memory, analytics-accuracy, and geographic-insights release candidate built on the rc4 privileged-helper protocol fix.

## Verified in the review environment

- Complete Go regression suite
- `go vet ./...`
- Go race detector
- Frontend TypeScript check and production build
- `npm audit --audit-level=high` with zero reported vulnerabilities
- Existing-database migration and idempotent retained-history rollup backfill
- Links/Analytics click-total consistency regression coverage
- Analytics deletion with max-click lifetime preservation
- GPC/DNT aggregate-only behavior
- HEAD/prefetch/social-preview exclusion
- Verified Cloudflare country/client headers, direct-header spoof rejection, optional IPinfo fallback, private/reserved-IP rejection, and no raw-IP persistence
- Rebuilt responsive/keyboard/touch map, consistent 24-hour peak chart, and filter UI
- Domain-scoped, exact-case slug uniqueness and host-bound redirect resolution
- Safe migration from the legacy global short-code unique constraint
- Seven-character cryptographic alias generation for blank slug and subdomain inputs
- API and UI protection for the current default domain
- Low-memory queue/cache/database settings and socket-activated privileged helper
- Native RSS benchmark evidence and systemd unit verification

## Historical-data note

The migration can backfill hourly aggregates only from detailed click rows that still exist. Clicks that were previously opted out, never recorded, or deleted by retention have no historical timestamp and cannot be assigned to an old hour without fabricating data. The all-time visible total remains accurate; post-upgrade hourly rollups are complete for accepted human redirects.

## Official binary requirements

An official deployment binary must:

1. Be built with the exact compiler pinned in `backend/go.mod` (Go 1.26.4).
2. Pass `make test`, `make vet`, and `make race`.
3. Embed the audited frontend assets in `backend/web`.
4. Publish a fresh per-file checksum manifest and build provenance.
5. Be signed before being represented as a trusted public release.

## User analytics merge verification

- Submitted `handlers_stats.go`, `Analytics.tsx`, and `Dashboard.tsx` changes reviewed and merged.
- Top-links unique counts are now produced by one bounded, user-scoped query rather than per-row database calls.
- Empty peak-hour data and disabled detailed-analytics states have explicit non-misleading UI behavior.
- Added range/deduplication regression coverage for top-link unique visitors.