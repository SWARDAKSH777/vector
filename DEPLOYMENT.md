# Production Deployment Checklist

## Before installation

- Use a dedicated, supported Ubuntu/Debian AMD64 host or VM.
- Patch the OS and restrict SSH to administrative networks or a VPN.
- Permit TCP 80/443 publicly. Permit TCP 8080 only temporarily during setup and only from an administrator IP when possible.
- Point the intended domain to the host. For wildcard subdomain links, create a least-privilege Cloudflare API token scoped only to required zones.
- Obtain the release through a trusted channel and verify the separately published bundle checksum/signature.

## Installation

```bash
sudo bash install.sh
```

Save the one-time bootstrap credential. Complete setup at the temporary port, confirm HTTPS, and remove any cloud/UFW/firewalld rule exposing port 8080.

## Verification

```bash
sudo systemctl status vector vector-helper.socket nginx certbot.timer --no-pager
sudo systemctl cat vector
sudo systemctl cat vector-helper.socket vector-helper.service
sudo nginx -t
/opt/vector/vector --version
go version -m /opt/vector/vector
curl -fsS https://YOUR_DOMAIN/healthz
sudo certbot renew --dry-run
```

Confirm the web service listens only on loopback:

```bash
sudo ss -lntp | grep 8081
```

## Backups

Stop Vector briefly or use SQLite’s online backup mechanism. Back up the database and master key together, encrypt backups, restrict access, keep off-host copies, define retention, and test restoration.

A simple stopped-service backup:

```bash
sudo systemctl stop vector
sudo tar -C / -czf vector-backup-$(date +%F).tar.gz \
  opt/vector/data etc/vector/master.key
sudo systemctl start vector
```

## Country analytics

Vector uses a Cloudflare-first country pipeline and does not require a local GeoIP database. For a request whose TCP peer is inside Cloudflare's official proxy ranges, Nginx normalizes `CF-Connecting-IP` and passes the validated two-letter `CF-IPCountry` value to Vector. Headers from direct clients are ignored even when they use the same names. Enable Cloudflare **Network → IP Geolocation**, or the **Add visitor location headers** Managed Transform, for proxied domains.

The installer places `/etc/nginx/conf.d/vector-cloudflare-trust-map.conf` and enables `vector-cloudflare-ips.timer`. The timer refreshes the official IPv4/IPv6 ranges weekly, validates every CIDR, runs `nginx -t`, restores the previous file on failure, and reloads Nginx only after a valid update.

IPinfo Lite is an optional fallback for direct traffic or a request that has no valid Cloudflare country header. Add the token from **Settings → Country Analytics**. The token is validated before replacement, encrypted with the external master key, never returned by the API, and can only be replaced or deleted.

A one-time migration clears only historical country assignments and the GeoIP cache because prior releases could resolve a shared Cloudflare edge address. It preserves clicks, visitor hashes, browsers, devices, operating systems, referrers, timestamps, links, and hourly totals. Historical countries cannot be reconstructed because raw visitor IPs were deliberately never retained.

## Monitoring

Alert on:

- `vector`, `vector-helper.socket`, on-demand `vector-helper.service`, or Nginx failures.
- Repeated authentication failures/rate limits.
- Certbot renewal failures and certificate expiry.
- Disk fullness, memory pressure, database errors, and backup failures.
- Unexpected checksum changes to installed binary/unit/configuration.
- Cloudflare API failures and domains in an error state.

## Capacity and availability

SQLite is suitable for a modest single-node workload but is not an HA datastore. Load test with expected redirect and analytics volume. For strict availability requirements, use an external durable database/queue architecture, multiple instances, distributed rate limits, and tested failover—features not included in this release.

### Low-memory profile

The installer writes `GOMAXPROCS=1`, `GOMEMLIMIT=64MiB`, `GOGC=50`, `GODEBUG=madvdontneed=1`, `DB_MAX_OPEN_CONNS=2`, and `DB_MAX_IDLE_CONNS=1` to `/etc/vector/runtime.env`. The helper is socket-activated and exits after 30 idle seconds. In the review environment, normal Vector plus one-worker Nginx RSS was about 45.4 MiB; temporary domain/certificate work raised it to about 68.9 MiB. Measure on the production host before setting hard cgroup limits.

## Upgrade

- Back up `vector.db` and `/etc/vector/master.key`.
- Verify the new release bundle.
- Run the new installer; it preserves data and the master key.
- Review migrations, service sandbox output, logs, Nginx, and `certbot renew --dry-run`.
- Keep a tested rollback binary and database backup.

## Privileged-helper and certificate ownership checks

Confirm `/run/vector-helper.sock` is owned by `root:vector` with mode `0660`, the web service has no sudoers entry, and managed certificates have root-only ownership markers under `/etc/letsencrypt/vector-credentials/*.owner`. Vector deliberately refuses to adopt or delete a pre-existing certificate without its marker.

## Release-toolchain requirement

Official binaries must be built using the Go version pinned in `backend/go.mod` (currently Go 1.26.4). `make release-linux` fails closed if the local compiler does not match. Record `go version -m vector-linux-amd64` in release evidence.
