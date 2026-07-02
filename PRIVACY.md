# Privacy and Data Handling

This document describes the software defaults. The operator remains responsible for publishing an accurate privacy notice, establishing a lawful basis, honoring applicable rights, and configuring retention for the actual deployment.

## Data processed

### Account data

- Administrator and regular-user email addresses and roles.
- Salted password verifier; plaintext passwords are not stored.
- Server-side session token digest, timestamps, and a user-agent digest.
- Security audit events with a keyed hash of the source IP and limited event metadata.

### Link data

- Destination URL, short code, domain, tag, notes, status, expiry, click limit, and optional salted password verifier.
- Cloudflare zone/record identifiers for DNS resources created by Vector.
- Cloudflare API tokens encrypted at rest. Certbot also requires a root-only plaintext credential file for unattended DNS-01 renewal under `/etc/letsencrypt/vector-credentials`.

### Analytics data

For each accepted human GET redirect, Vector increments a link-level visible counter and an anonymous hourly aggregate. These aggregates contain only link ID, UTC hour, and count. They do not contain an IP address, visitor identifier, user agent, referrer, or location. HEAD requests, browser prefetch/prerender requests, and recognized search/social preview crawlers are excluded.

When detailed analytics are enabled, ordinary requests may additionally retain:

- Click timestamp.
- Device and browser category derived from the user agent; the raw user-agent string is not retained.
- Referrer origin only; path, query, and fragment are discarded.
- Two-letter country code supplied by a verified Cloudflare proxy request or, when needed, returned by the optional IPinfo Lite fallback. City, region, coordinates, ASN, and organization data are not requested or retained.
- A keyed pseudonymous visitor value derived from the trusted client IP path; the raw IP is not retained by Vector.

For verified Cloudflare traffic, the country is supplied in the request path after Nginx confirms the peer belongs to Cloudflare. For direct traffic or a missing country header, the public IP may be transmitted transiently to the optional IPinfo Lite fallback over HTTPS. Vector stores a keyed cache identifier and country code, never the raw IP. Redirects do not wait for the fallback provider.

## Defaults

- Analytics enabled: yes.
- Detailed analytics retention: 90 days.
- Anonymous hourly rollups: retained until analytics are explicitly deleted or the associated link is deleted.
- Security audit retention: 365 days.
- Session absolute lifetime: 24 hours.
- Session idle lifetime: 2 hours.
- GPC and DNT: honored for click analytics.

The administrator can disable detailed analytics and adjust global retention. Each signed-in user can delete analytics for their own links and export their own account, owned/shared-domain relationship, and link data in Settings. Deleting analytics removes that user's detailed events and aggregate rollups and resets the click totals displayed on Links. When the administrator performs the deletion, Vector also clears the deployment-wide pseudonymous country cache and in-flight/in-memory lookup state; regular users cannot modify that shared cache. It intentionally preserves a separate internal lifetime count used only to enforce max-click limits, preventing a privacy deletion from reactivating an exhausted link.

## Account deactivation and domain sharing

Administrator “delete” is implemented as soft deactivation. It sets the account inactive and revokes server-side sessions, but retains the email address, password verifier, links, domains, domain memberships, analytics, and audit history. This preserves live redirects and ownership records. Operators must use content-level deletion, retention procedures, or full deployment purge when legal erasure is required.

A shared-domain membership records the domain, user, role (`owner` or `member`), creation time, and that user's default-domain choice. Shared members do not receive the domain owner's Cloudflare token. Removing membership prevents future use but deliberately leaves previously created links and their analytics intact under the creator's account.

## Data minimization and deletion

Expired detailed analytics and audit events are deleted automatically. Anonymous hourly rollups are not visitor-level records and remain available for long-range totals until the administrator deletes analytics or deletes the associated link. Deleting a link cascades to its click records. Deleting the deployment with `vector-total-purge.sh` removes the application database, master key, service configuration, managed certificates, and local secrets. Cloudflare DNS records are intentionally not removed by the purge script; authenticated application deletion should be used first when DNS cleanup is required.

## Backups and logs

Backups, system journals, reverse-proxy access logs, cloud-provider logs, and monitoring systems have independent retention and deletion requirements. Vector’s application controls do not delete copies held in those systems.

## Cross-border processing and subprocessors

Vector itself does not choose hosting, DNS, certificate, backup, or monitoring providers. When an IPinfo Lite fallback token is configured in Settings, IPinfo is an additional analytics subprocessor that may receive public visitor IP addresses only when a valid Cloudflare country is unavailable. The operator must review the provider terms and retention practices and document all providers, locations, transfer mechanisms, contracts, and subprocessors in its privacy materials.

## Analytics v2 event storage

New click analytics use the `analytics_events` table. For ordinary requests it stores a link ID, UTC time, HMAC-based pseudonymous visitor value, origin-only referrer, coarse device/browser/operating-system labels, and an optional two-letter country code. It does not store raw IP addresses or full user-agent strings.

Requests carrying Global Privacy Control or Do Not Track store only the link ID, UTC time, and fixed `Privacy-protected` labels; their visitor hash, client IP, country, real referrer, and user-agent classification remain empty. They are excluded from unique-visitor, repeat-rate, and geo calculations.

Country resolution uses a verified Cloudflare country header when available. Otherwise it may send the transient public IP to the configured IPinfo Lite fallback and stores only the resulting country code.
