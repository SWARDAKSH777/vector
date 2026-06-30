# Threat Model

## Assets

- Administrator account and sessions.
- Destination URLs, notes, tags, analytics, and audit events.
- Cloudflare tokens, DNS records, certificates, and private keys.
- SQLite database and `/etc/vector/master.key`.
- Nginx configuration and the privileged helper boundary.

## Main trust boundaries

1. Internet client to Nginx.
2. Nginx to loopback-only Vector web service.
3. Vector web service to SQLite and encrypted secrets.
4. Vector web service to the root helper over `/run/vector-helper.sock`.
5. Root helper to Nginx, Certbot, Let’s Encrypt, and Cloudflare DNS.
6. Host/backups/release channel controlled by the operator.

## Addressed threats

- Public takeover of initial setup: one-time bootstrap authentication.
- Credential guessing: strong password policy, timing-resistant verification, IP/account rate limits.
- Session theft/fixation: random server-side tokens, revocation, idle/absolute expiry, secure cookie attributes, HTTPS rotation.
- Cross-site request forgery: SameSite cookies, CSRF token, Origin/Referer validation.
- Injection and malformed requests: strict JSON, size limits, hostname/port/email/token/URL validation, parameterized SQL.
- Root command injection: no shell execution of user input; constrained helper actions and arguments.
- DNS deletion of operator-managed records: exact record-ID ownership table.
- Partial DNS/link state: DNS-first transaction and rollback.
- Password verifier disclosure: opaque signed unlock grants.
- Excessive analytics collection: no raw IP/user-agent/full referrer, GPC/DNT, retention and deletion.
- Stale/broken TLS: persistent root-only DNS credentials, Certbot timer and reload hook.

## Residual risks

- A host/root compromise defeats application controls.
- A compromised `vector` process can request the helper's narrowly supported operations, but the helper authenticates the exact Unix peer UID, validates the configured backend port, and refuses unmanaged certificate ownership.
- In-memory rate limits reset on restart and do not coordinate across nodes.
- No MFA, centralized identity, destination reputation service, anti-phishing moderation, or tenant isolation.
- SQLite and a single host remain availability bottlenecks.
- Bundled checksum manifests do not prove publisher identity without an external signature/trusted checksum.
- Analytics visitor hashes are pseudonymous and may be regulated data.
- Business-logic abuse, malicious destinations, domain reputation, and legal takedown handling require operational processes.
