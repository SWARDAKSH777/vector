# Recommended Future Capabilities

These are recommendations only and are not implemented in 6.0.0-rc1.

## Highest priority for enterprise use

- WebAuthn/passkeys or TOTP MFA; OIDC/SAML SSO; SCIM; role-based access control; multiple administrators with approval workflows.
- PostgreSQL-backed multi-instance deployment, distributed rate limiting, durable background jobs, high availability and tested disaster recovery.
- External KMS/HSM or cloud secret manager for the master key and Cloudflare token rotation.
- Signed releases, protected CI, reproducible builds, SLSA provenance, automated SBOM/vulnerability scans and dependency-update policy.
- Centralized tamper-evident audit export, SIEM integration, alert rules and documented incident-response runbooks.

## Abuse and public-link safety

- Destination reputation/phishing/malware checks, abuse reports, takedown workflow, domain reputation monitoring and optional administrator approval for public links.
- CAPTCHA or proof-of-work on password unlock after repeated failures, plus an external WAF/CDN policy.
- Per-link/domain traffic quotas, anomaly detection and configurable geographic/network restrictions.

## Operations and product maturity

- Prometheus/OpenTelemetry metrics and tracing, structured JSON logs, health/readiness separation and synthetic certificate/redirect monitoring.
- API keys with scopes, webhooks, bulk administration, retention/legal-hold controls and immutable audit export.
- Formal backup API, online consistent snapshots, restore verification and migration rollback tooling.
- Accessibility audit, localization, browser compatibility matrix and documented capacity benchmarks.
