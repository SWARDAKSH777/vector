# User Analytics Merge Report

Date: 2026-06-26

## Submitted changes retained

- Correct empty-data behavior for peak-hour analytics.
- Hide unique-visitor chart series when detailed analytics is disabled.
- Improve Dashboard and Analytics labels when detailed analytics is disabled.
- Remove the top-links unique-visitor N+1 query pattern.

## Corrections made during review

- The submitted correlated unique-visitor subquery was replaced by a bounded two-stage CTE. The first stage selects at most ten top links belonging to the authenticated user. The second stage counts distinct non-empty visitor hashes only for those selected links and only inside the requested time range. This avoids one query per row and avoids scanning unrelated users' link IDs.
- Unique visitors and repeat rate now render as `N/A`, not `0`, while detailed analytics is disabled. A zero would incorrectly imply a measured result.
- Added deterministic ordering for equal click totals.
- Added regression coverage that verifies duplicate visitor hashes are deduplicated, empty hashes are excluded, out-of-range events are excluded, click ordering is correct, and visible all-time counters are preserved.

## Security review of the submitted files

- Analytics SQL values remain parameterized.
- The only dynamic grouped-stat column remains server-selected from fixed handler calls (`device` or `browser`); request input cannot select a SQL identifier.
- Every analytics query remains scoped to the authenticated user's links.
- React continues to escape displayed domain, slug, destination, referrer, and country values. No raw HTML insertion was introduced.
- External links retain `noopener`/`noreferrer`.
- No secret, token, raw visitor IP, privileged-helper, authentication, or authorization behavior was weakened.

## Validation

- `go test ./...` passed.
- `go vet ./...` passed.
- `go test -race ./...` passed.
- Frontend strict TypeScript/Vite production build passed.
- `npm audit --audit-level=high` reported zero known vulnerabilities.
- Installer and purge shell syntax checks passed.
- Embedded frontend assets were rebuilt and copied into the backend.
