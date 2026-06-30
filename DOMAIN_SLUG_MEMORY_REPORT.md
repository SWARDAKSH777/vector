# Domain-scoped slugs and memory report

Review date: 2026-06-26

## Link identity and routing

Vector now treats a link as the tuple:

```text
(lowercase domain, redirect type, exact-case slug)
```

Consequences:

- `https://xx.example.com/test` and `https://yy.example.com/test` can point to different destinations.
- `https://example.com/Test` and `https://example.com/test` can coexist and resolve independently.
- Domain names remain case-insensitive and are normalized to lowercase.
- Subdomain prefixes remain lowercase because DNS names are case-insensitive.
- The database migration preserves link IDs, clicks, rollups, managed DNS ownership, and other link metadata while replacing the old global short-code uniqueness rule with a composite exact-case index.

A blank alias now generates a cryptographically random seven-character alphanumeric value. Slug links use the full `0-9a-zA-Z` alphabet; generated subdomain prefixes use `0-9a-z`. Rejection sampling avoids modulo bias, and database uniqueness is checked within the selected domain and redirect type.

## Default-domain protection

The current default domain cannot be deleted from either the API or the interface. To remove it, first make another active domain the default. The primary origin and final active-domain protections remain in place as separate safeguards.

## Low-memory changes

- One Go scheduler thread by default (`GOMAXPROCS=1`).
- Go soft memory limit of 64 MiB and more frequent collection (`GOMEMLIMIT=64MiB`, `GOGC=50`).
- Immediate page release hint on Linux (`GODEBUG=madvdontneed=1`).
- SQLite pool reduced to two open connections and one idle connection.
- Country lookup reduced to one worker, a 256-job queue, and a bounded 512-entry memory cache.
- Pending country enrichment IDs are capped at 2,048 per IP cache key.
- HTTP idle connection pools are deliberately small.
- Rate-limit and alias-reservation cleanup is lazy instead of using permanent cleanup goroutines.
- The privileged domain/certificate helper uses systemd socket activation and exits after 30 idle seconds, so it consumes no steady-state process RAM.

## Measured memory

Measurements were taken in the supplied Linux review container using the locally compiled AMD64 candidate, IPinfo country resolution enabled with an inert token, embedded production frontend assets, SQLite WAL, and the low-memory environment above. RSS naturally varies with kernel, libc, traffic, TLS, database size, and Go version.

| Component/state | Measured RSS |
|---|---:|
| Vector web process, median of five warm runs | **31.5 MiB** |
| Vector web process, observed range | 28.2–33.5 MiB |
| Nginx, one master plus one worker | **13.9 MiB** |
| Normal steady-state total | **about 45.4 MiB** |
| Privileged helper while temporarily active | **about 23.5 MiB** |
| Temporary total during domain/certificate work | **about 68.9 MiB** |

A synthetic 5,000-request health-endpoint run at concurrency 25 produced no failed requests and observed approximately 31.3 MiB peak Vector-web RSS in this environment. This is a smoke benchmark, not a production capacity guarantee.

The earlier unbounded/default runtime profile measured a 39.2 MiB median web RSS in the same environment. The new web profile is approximately 20% lower, and socket activation removes the helper's roughly 23.5 MiB from normal idle operation.

## Native versus Docker

For this release, native systemd deployment is recommended:

- The current helper intentionally performs tightly validated host Nginx and Certbot operations. Containerizing that helper would require sensitive host mounts or a broader redesign.
- A Linux container runs the same Vector process on the host kernel, so Docker does not make the application process use less memory.
- If Docker is not already installed, `dockerd` and `containerd` add host-wide memory that is not present in the native deployment. This review environment did not have Docker installed, so no fabricated daemon-memory number is reported.
- Containers make replacement of application files convenient, but persistent volumes deliberately survive container deletion. A true purge must explicitly remove the database volume, secrets, certificates, and host proxy configuration.
- The supplied native `vector-total-purge.sh` already provides an explicit full-removal path without adding a container runtime.

Docker becomes attractive only after separating TLS/DNS provisioning from Vector—for example, by managing the reverse proxy and certificates externally and running Vector as a non-root application-only container. In that architecture, use a persistent volume for `/opt/vector/data`, mount the master key read-only, run as a non-root UID, use a read-only root filesystem, drop all capabilities, and apply an explicit memory limit.
