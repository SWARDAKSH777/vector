> **Historical document:** this audit predates the final rc5 verification and contains superseded release-integrity and validation claims. Use `SECURITY_AUDIT_FINAL.md` as the current result.

# Vector 6.0.0-rc1 Security and Production Readiness Audit

Audit date: 2026-06-26

## Scope

This review covered the Go HTTP service, React frontend, SQLite schema and migrations, Cloudflare/DNS workflows, Nginx and Certbot provisioning, privileged helper, installer, purge script, privacy controls, release integrity, dependencies, and operational documentation. It was a source-assisted engineering review with automated tests; it was not an independent penetration test or formal certification.

## High-impact findings remediated

| Finding | Resolution |
|---|---|
| Public initial setup takeover | Installer-generated one-time bootstrap credential; all setup mutations require a validated bootstrap session. |
| Web process had broad passwordless root/Nginx/Certbot access | Replaced with a separate root helper over a local Unix socket; exact peer UID, action, hostname, port, token and ownership validation. |
| Browser setup sessions failed or were unsafe over temporary HTTP | Separate temporary setup cookie, rotation to `__Host-` HTTPS cookie, server-side sessions, absolute/idle expiry and revocation. |
| No CSRF protection | Session-bound HMAC CSRF tokens, strict same-origin checks, and authenticated token issuance. |
| Domain/link creation could succeed after Cloudflare failure | DNS is created and verified before database insertion; invalid/revoked tokens fail closed and mark domain unhealthy. |
| DNS cleanup could delete records Vector did not create | Exact Cloudflare record-ID ownership is stored and only owned records are deleted. |
| Certificate provisioning could adopt/delete unrelated certificates | Root-only ownership markers and Vector-managed Nginx markers are required for adoption/deletion. |
| Certbot renewal credentials were ephemeral | Persistent root-only DNS credential files plus validated Nginx reload deploy hook. |
| Link update bypassed create-time URL/password validation | Shared validation is applied to create and update; only HTTP(S) destinations are accepted. |
| Password-protected links exposed verifier material in cookies | Opaque signed, short-lived unlock grants; per-link and global brute-force throttles. |
| Slugs/unlocks could be replayed on another managed host | Redirect and unlock resolution are bound to the selected exact/wildcard host. |
| Dashboard/setup exposed on wildcard and non-default domains | Production middleware restricts those hosts to their exact redirect/unlock surface only. |
| Raw IP/full referrer/user-agent analytics | Keyed pseudonymous visitor IDs, referrer origin only, categorized device/browser, configurable retention, GPC/DNT support. |
| Click limit races | Click-limit check and increment are atomic. |
| Weak or ignored random/error paths | OS entropy failures propagate; migrations, commits, integrity checks and sensitive-state writes fail closed. |
| Stale or ambiguous domain state | Only active domains can become defaults; primary origin/final active domain cannot be deleted; provisioning/deletion is idempotent. |
| Release files could be silently altered | Installer verifies every bundle entry in `MANIFEST.sha256`, warns on missing/changed/malformed entries, and continues by design for open-source modifications. |

## Defense-in-depth added

- Loopback-only application listener behind Nginx.
- Restrictive HTTP timeouts, header/body limits and strict JSON parsing.
- PBKDF2-HMAC-SHA-256 with 600,000 iterations, random salts, password length/size/common-password controls and legacy migration.
- AES-256-GCM secret encryption with a root-managed external master key.
- Hardened cookies, HSTS after HTTPS, CSP, frame denial, MIME sniffing protection, referrer and permissions policies.
- Parameterized SQL, SQLite WAL, foreign keys, `secure_delete`, `trusted_schema=OFF`, startup integrity checks and uniqueness constraints.
- Security audit records, privacy export, analytics deletion and retention cleanup.
- Hardened systemd services with no capabilities for the web process and narrowly writable helper paths.
- Manifest, SBOM, third-party notices, threat model, security policy, privacy guide and compliance control mapping.

## Validation performed

- `go test ./...`: pass.
- `go vet ./...`: pass.
- `go test -race ./...`: pass.
- Security regression tests: pass, including CSRF, session/cookie behavior, host isolation, DNS-before-link, invalid-token rollback, Nginx policy, migration integrity and entropy failure.
- Frontend production build: pass.
- `npm audit --audit-level=high`: 0 known vulnerabilities at audit time.
- Installer and purge script syntax checks: pass.
- Git whitespace/error check: pass.
- Release manifest positive and modified-file warning tests: required during packaging.

## Open release blockers

1. **Compiler provenance:** official public binaries must be rebuilt in trusted CI with the Go version pinned in `backend/go.mod` (Go 1.26.4). The supplied local candidate was built with Go 1.23.2 because this environment could not fetch the current toolchain. The Makefile rejects that compiler for official releases.
2. **Independent assurance:** no third-party penetration test, external attack-surface test, or formal code-signing/provenance review has been completed.
3. **Security contact:** the project owner must publish a monitored private vulnerability-reporting channel and response targets.
4. **Release authenticity:** the manifest detects modifications but does not authenticate the publisher. Sign the release and publish its checksum/signature through a trusted channel.
5. **Operational evidence:** perform a real backup/restore test, `certbot renew --dry-run`, load test, firewall review, alerting test and incident-response exercise on the production host.

## Enterprise/compliance conclusion

The source is substantially hardened for a single-node, single-administrator deployment and contains useful privacy and evidence controls. It is **compliance-supporting, not compliance-certified**. It is not enterprise-ready for organizations requiring MFA/SSO/RBAC, multiple administrators/tenants, HA, external KMS/HSM, centralized tamper-evident logging, distributed rate limiting, formal support/SLA, signed updates, or certified controls.

## Analytics accuracy and privacy review (rc5)

- Split resettable displayed analytics from the non-resettable max-click enforcement counter.
- Made redirect counting and hourly aggregation one atomic SQLite transaction.
- Added anonymous rollups so GPC/DNT does not create misleading public click totals while still suppressing visitor-level data.
- Excluded non-human preview and prefetch requests from counters.
- Made Cloudflare visitor-IP headers explicitly opt-in and documented the Cloudflare-only origin firewall boundary; authentication and abuse controls continue to use the trusted peer/proxy path.
- Added country-only IPinfo Lite enrichment with a bounded queue, strict timeouts, limited retries, circuit breaking, HMAC-keyed caching, private/reserved-IP rejection, provider-response validation, and no raw-IP persistence.
- Made analytics deletion cancel in-flight country requests and clear memory and persistent country caches.
- Added migration, deletion, race, and cross-page consistency tests.

Remaining limitation: user-agent bot identification is heuristic. An adversarial crawler can imitate a browser, and no self-hosted counter can guarantee perfect human-only attribution without stronger edge/browser challenges.
## Domain-scoped slug and memory review (rc5 update)

- Replaced global short-code uniqueness with a composite domain, redirect-type, and binary-collated slug index.
- Kept DNS names normalized and case-insensitive while preserving exact slug case in validation, storage, availability checks, redirect lookup, and password unlock lookup.
- Rebuilt legacy `links` tables with foreign keys temporarily disabled on a single startup connection, preserving primary keys and checking referential integrity after migration.
- Used OS entropy and unbiased rejection sampling for automatic seven-character aliases; entropy errors fail closed.
- Enforced default-domain deletion protection in the backend rather than relying only on a disabled interface control.
- Bounded country queues, memory caches, pending click lists, HTTP idle pools, and SQLite connections to reduce denial-of-service amplification and RSS.
- Replaced the permanently resident root helper with a hardened systemd socket-activated service that exits after an idle timeout.

Measured steady-state RSS in the review environment was approximately 45.4 MiB for Vector plus a one-worker Nginx. The helper adds approximately 23.5 MiB only while active. These measurements are environment-specific and are not hard safety limits.

