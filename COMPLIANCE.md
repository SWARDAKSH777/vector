# Compliance Readiness Guide

Vector provides technical controls that can support a compliance program; installing it does not make an organization compliant or certified. Legal counsel, documented policies, evidence collection, risk assessment, vendor management, incident response, and independent audits remain organizational responsibilities.

## Control mapping

| Area | Implemented technical support | Operator evidence/actions still required |
|---|---|---|
| Access control | One-time protected setup, strong password policy, rate limiting, server-side sessions, password rotation revokes sessions | MFA/SSO strategy, joiner-mover-leaver process, periodic access review |
| Least privilege | Local-only app listener, unprivileged web service, constrained root helper, hardened systemd units | Host hardening baseline, SSH/IAM review, cloud security groups |
| Secrets | AES-GCM application-token encryption, external master key, root-only Certbot credentials | Key escrow/rotation procedure, external KMS for higher assurance, secret scanning |
| Change integrity | Source available, warning-only manifest verification, bundle checksum, SBOM | Signed releases, protected CI/CD, approvals, provenance, reproducible-build evidence |
| Logging | Security audit events and configurable retention | Central collection, alerting, clock synchronization, restricted/tamper-evident storage |
| Privacy | Data minimization, no raw IP retention, GPC/DNT, retention controls, export and analytics deletion | Privacy notice, lawful basis, request workflow, DPA/ROPA, backup deletion policy |
| Availability | Graceful shutdown, health endpoint, SQLite WAL, service restart | HA/DR design, monitored backups, restore tests, capacity tests, RTO/RPO |
| Network/TLS | Nginx reverse proxy, TLS 1.2/1.3, automated Certbot renewal | External TLS monitoring, DNS security, firewall review, certificate-expiry alerts |
| Secure development | Input validation, CSRF, secure cookies, tests, dependency audit, SBOM | Code review policy, independent pentest, vulnerability-management SLA |

## GDPR-oriented readiness

The software supports data minimization, purpose-limited analytics configuration, retention, export, and deletion. The operator must determine controller/processor roles, lawful basis, consent requirements where applicable, data-subject request procedures, breach notification, international transfers, and contracts. Pseudonymous visitor hashes may still be personal data.

## SOC 2 / ISO 27001-oriented readiness

Vector can provide a portion of application-level evidence, but enterprise controls such as identity governance, asset management, supplier review, incident exercises, change approvals, centralized monitoring, business continuity, policy ownership, training, and independent audit evidence are outside the product.

## PCI DSS / HIPAA and other regulated use

Vector is not specifically validated for PCI DSS, HIPAA, FedRAMP, financial-services regulations, or government security baselines. Do not place payment-card data, protected health information, authentication secrets, or similarly sensitive data in short-link URLs, tags, or notes without a separate scoped assessment and required contractual/technical controls.

## Launch evidence checklist

- Recorded threat model and risk acceptance.
- Independent penetration-test report with findings remediated.
- Dependency/SBOM review and release checksum/signature.
- Backup and restore evidence.
- Certificate renewal test and monitoring evidence.
- Privacy notice, retention decision, and deletion/export procedure.
- Security contact, incident response process, and vulnerability response targets.
- Host/cloud hardening and firewall evidence.

## Analytics-control notes (rc5)

- Detailed visitor-level analytics can be disabled independently of anonymous hourly click aggregates. Detailed events follow the configured retention period; aggregate rollups remain until explicit analytics/link deletion and should be covered by the operator's retention policy.
- GPC and DNT suppress detailed event creation while the aggregate redirect count remains available for operational accuracy.
- Analytics deletion removes detailed events and aggregates and resets user-visible click counters.
- A separate internal lifetime count is retained solely to enforce configured maximum-click limits; this prevents deletion from weakening an access-control/business rule. Operators should disclose this limited retained counter where applicable.
- Country codes depend on the configured IPinfo Lite service. Public visitor IPs are transmitted to that provider for lookup, so IPinfo must be included in the deployment data inventory, subprocessor assessment, privacy notice, and transfer/retention review.
