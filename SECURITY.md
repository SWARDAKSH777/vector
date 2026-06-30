# Security Policy

## Supported release

Security fixes are maintained for the latest published release. Upgrade promptly and keep both the database and `/etc/vector/master.key` during upgrades.

## Reporting a vulnerability

**Do not open a public issue with exploit details, credentials, or unpatched proof-of-concept code.**

Open a [GitHub private security advisory](../../security/advisories/new) or email the address listed in the repository's GitHub Security tab. Include: affected version, impact, reproduction steps, and suggested remediation.

Expected response targets: acknowledgement within 2 business days, triage within 5 business days, patch for critical issues within 14 days.

## Security assumptions

- The host OS, root account, Nginx, Certbot, Cloudflare account, DNS registrar, and release distribution channel are trusted.
- Vector is a single-node, single-administrator SQLite deployment. It is not designed for multi-tenant or multi-administrator use.
- Compromise of root, the `vector` Unix account, the database together with master.key, or the Cloudflare account can compromise the deployment.

## Operator responsibilities

- Verify release checksums before installing.
- Restrict SSH to key auth only, keep OS/Nginx/Certbot updated, and close TCP 8080 after initial setup.
- Use a least-privilege Cloudflare API token (Zone Read + DNS Edit only).
- Back up and protect `/etc/vector/master.key`; never commit it or the database to version control.
- Monitor authentication failures, audit events, and certificate renewal.
- Run an independent penetration test before handling sensitive or regulated traffic.

## Built-in security controls

- **Password hashing:** Argon2id (time=3, memory=64 MiB, parallelism=2) for all new and migrated hashes. Existing PBKDF2 and bcrypt hashes are verified and transparently re-hashed to Argon2id on next login.
- **Token encryption:** AES-256-GCM with a root-managed external master key for Cloudflare API tokens.
- **Sessions:** Server-side session tokens, `__Host-` cookies, HttpOnly/Secure/SameSite=Strict, absolute (24 h) and idle (2 h) expiry, concurrent session limit (10).
- **CSRF:** HMAC-bound per-session tokens.
- **Rate limiting:** Per-IP and per-account login throttling; bounded rate-limiter maps.
- **SQL:** Parameterised queries throughout; SQLite WAL, foreign keys, secure_delete, trusted_schema=OFF.
- **Transport:** Loopback-only listener behind Nginx; HSTS; strict CSP; X-Frame-Options DENY.
- **Analytics privacy:** Keyed pseudonymous visitor IDs; no raw IP persistence; referrer origin only; GPC/DNT support; configurable retention.
- **Privileged helper:** Socket-activated, peer-UID-verified root helper for Nginx/Certbot operations; exits after inactivity.
- **Proxy authentication:** Nginx injects a shared secret (`/etc/vector/proxy.key`) verified in constant time; forwarded headers are ignored from untrusted sources.

## Not yet included

MFA/WebAuthn, SAML/OIDC, RBAC, multi-tenancy, external KMS/HSM, distributed rate limiting, HA, signed update delivery, and formal certification are out of scope for this release.
