# Vector v6.0.0-rc5 Security Hardening Report

Date: 2026-06-26

## Scope

The review covered the Go HTTP service, authentication/session handling, tenant ownership checks, SQLite queries and migrations, the root privileged helper, Unix socket boundary, Nginx/Certbot integration, Cloudflare and IPinfo clients, secret storage, frontend rendering, systemd sandboxing, installer/update supply chain, and purge tooling.

No software can honestly be guaranteed free of every present or future vulnerability. This release fixes all exploitable issues identified during this review and adds regression tests for the highest-risk trust boundaries.

## Critical and high-risk fixes

### Authenticated reverse-proxy boundary

Previously, any process able to connect to `127.0.0.1:8081` could claim to be Nginx by supplying `X-Forwarded-For` or `X-Forwarded-Proto`. This could falsify source IPs, weaken rate limiting, and influence HTTPS/session decisions.

The installer now creates `/etc/vector/proxy.key` as `root:vector` mode `0640`. Nginx injects the key through `X-Vector-Proxy-Auth`; Vector accepts forwarded headers only when the connection is loopback and the header matches in constant time. Vector-managed Nginx files containing the key are root-only mode `0600`.

### Explicit administrator authorization

The previous schema authenticated a user but did not encode an authorization role. A stray or legacy user record with a session could therefore inherit all management endpoints.

The database now stores `role` and `disabled`. Migration selects exactly one existing administrator, disables all other legacy users, revokes their sessions, and creates a partial unique index that permits only one active administrator. Session validation joins the user record on every request and rejects disabled or non-admin accounts.

### Installer integrity failure

The old installer treated missing or mismatched checksums as warnings and could subsequently execute a modified bundled binary as root.

Installation now fails closed when the manifest is missing, malformed, incomplete, contains unsafe paths/symlinks, or has any checksum mismatch. Security-sensitive installer, binary, and systemd files must all be covered by the manifest. The preserving upgrader separately verifies the source archive and downloads the pinned Go toolchain over HTTPS with the official SHA-256 checksum.

### Root helper confinement

The root helper now:

- accepts only the exact `vector` service UID through `SO_PEERCRED`;
- permits at most four concurrent operations and a 32 KiB request;
- accepts only fixed actions and validated domains/ports/emails/tokens;
- refuses symlink or non-regular managed files and directories;
- refuses to replace/remove files without Vector ownership markers;
- checks existing Nginx `server_name` entries before provisioning to avoid hijacking another local virtual host;
- uses absolute command paths and a minimal sanitized environment for Certbot, Nginx, and systemctl;
- writes Nginx and credential files atomically with root-only permissions;
- remains socket-activated and exits after inactivity.

The helper systemd unit adds invisible `/proc`, native syscall architecture restriction, memory-write/execute denial, a restrictive umask, and bounded socket backlog/connections.

## Additional fixes

- Login throttling now has a true account-wide limit independent of IP, preventing distributed brute-force bypass.
- All in-memory rate-limiter maps are bounded to prevent unique-key memory exhaustion.
- Alias-reservation input and map size are bounded.
- Administrator API/UI requests are rejected on direct-IP, localhost, or untrusted local connections after setup; public redirect paths remain available.
- Concurrent administrator sessions are limited to the ten newest tokens.
- Cloudflare requests disallow redirects, require TLS 1.2+, use bounded pools/timeouts, validate API identifiers, cap response sizes, sanitize provider errors, and bound DNS cache entries.
- IPinfo validation failures no longer expose raw provider responses to the browser; only a bounded sanitized reason enters the audit record.
- Mutable public-base-URL state is synchronized to eliminate a data race.
- Added `X-Permitted-Cross-Domain-Policies: none` and preserved strict CSP/frame/content policies.
- Cloudflare tokens are capped at 512 printable bytes.

## Validation

Completed in the review environment:

- `go test ./...`
- `go vet ./...`
- `go test -race ./...`
- frontend `npm ci --ignore-scripts`
- frontend `npm audit --audit-level=low` — zero known vulnerabilities at audit time
- frontend production build
- shell syntax checks
- source checksum verification
- regression tests for proxy spoofing, non-admin sessions, bounded rate limiters, Nginx conflicts, domain-scoped case-sensitive slugs, analytics deletion, token secrecy, migration, CSRF, and helper protocol validation

The local environment could not download `govulncheck` because outbound DNS was unavailable. The release upgrader builds with pinned Go 1.26.4, which includes the current Go security fixes as of this release date. The Go module has no remote runtime Go dependency; the SQLite driver and QR implementation are included in the source tree. Operators should continue applying future Go, OS, Nginx, Certbot, and npm security updates.
