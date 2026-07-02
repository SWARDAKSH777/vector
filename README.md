<div align="center">

# Vector URL Shortener

**Self-hosted URL shortener with custom domains, analytics, QR codes, and Cloudflare-assisted DNS/TLS provisioning.**

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26.4-00ADD8?logo=go)](https://go.dev)
[![Security](https://img.shields.io/badge/Security-Argon2id-green)](SECURITY.md)

</div>

---

## Features

- **Short links** — slug-based (`domain.com/code`) and subdomain-based (`code.domain.com`) with custom aliases
- **Custom domains** — automated DNS record creation and Let's Encrypt TLS via Cloudflare API
- **Optional multi-user tenancy** — administrator-managed accounts with owner-controlled shared-domain access
- **Analytics** — click timeseries, unique visitors, device/browser/OS breakdown, referrer traffic, peak hours, and an interactive world map
- **Country tracking** — Cloudflare-first (zero latency), IPinfo Lite fallback, no raw IP stored
- **QR codes** — generated server-side, no external service
- **Link controls** — expiry date, click limit, password protection
- **Privacy-first** — pseudonymous visitor IDs, GPC/DNT support, configurable retention, data export and deletion
- **Security** — Argon2id password hashing, AES-256-GCM token encryption, CSRF protection, rate limiting, hardened systemd units

---

## Requirements

| | Minimum |
|---|---|
| **OS** | Ubuntu 22.04 LTS or Debian 12 (amd64) |
| **RAM** | 512 MB (1 GB recommended) |
| **Disk** | 2 GB |
| **Domain** | A domain you control, pointed at the server's IP |
| **Cloudflare** | Required for automated DNS/TLS provisioning |
| **Port** | 80 and 443 open publicly; 8080 open temporarily during setup |

> **Note:** Vector supports either **single-user** or **multi-user** mode on one node. Multi-user mode isolates accounts at the application/database layer, but it is not horizontal scaling or a substitute for separate hosts where strict infrastructure-level isolation is required.

---

## Installation

### Step 1 — Prepare your server

```bash
# Update the system
sudo apt update && sudo apt upgrade -y

# Allow ports 80, 443, and 8080 (if using ufw)
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 8080/tcp   # only during setup — close this afterward
sudo ufw enable
```

Point your domain's A record to the server's public IP. This must resolve before installation so that Let's Encrypt can issue a certificate.

### Step 2 — Get a Cloudflare API token

1. Log in to [dash.cloudflare.com](https://dash.cloudflare.com)
2. Go to **My Profile → API Tokens → Create Token**
3. Use the **Edit zone DNS** template
4. Scope it to the specific zone(s) you plan to use
5. Set permissions: **Zone — Zone — Read** and **Zone — DNS — Edit**
6. Copy the token — you'll enter it in the setup wizard

### Step 3 — Download and verify the bundle

```bash
# Download the release
wget https://github.com/SWARDAKSH777/vector/releases/download/v6.0.0-rc14/vector-release.tar.gz

# Verify the checksum (replace with published hash)
sha256sum vector-release.tar.gz

# Extract
tar -xzf vector-release.tar.gz
cd vector-*
```

### Step 4 — Run the installer

```bash
sudo bash install.sh
```

The installer first asks which tenancy mode to use:

- **Single-user** — only the administrator can sign in; no user-management panel is exposed.
- **Multi-user** — the administrator can create, deactivate, reactivate, and reset regular-user accounts.

For unattended installation, set the choice explicitly:

```bash
sudo VECTOR_DEPLOYMENT_MODE=multi bash install.sh
```

The installer will:
- Install Vector and a socket-activated privileged helper as systemd services
- Create a dedicated `vector` system user
- Generate a root-managed encryption key at `/etc/vector/master.key`
- Generate a Nginx-to-backend shared proxy authentication key at `/etc/vector/proxy.key`
- Configure Nginx as a reverse proxy with security headers
- Print a **one-time bootstrap credential** — copy it before the screen clears

> **Save your bootstrap credential.** It is shown exactly once and cannot be recovered. If you lose it, run sudo vector-bootstrap-reset to generate new credentials or re-run the installer itself.

### Step 5 — Complete setup in the browser

Open `http://YOUR_SERVER_IP:8080` in your browser.

1. Enter the bootstrap credential when prompted
2. Set your administrator email and password (minimum 15 characters)
3. Enter your domain name (e.g. `links.example.com`)
4. Enter your Cloudflare API token
5. Vector will create DNS records and obtain a Let's Encrypt certificate
6. Once HTTPS is confirmed, you will be redirected to `https://YOUR_DOMAIN`

In multi-user mode, the administrator can open **Users** to create regular accounts. Every user can add domains using their own Cloudflare token.

### Step 6 — Close port 8080

Once setup is complete, close the temporary port:

```bash
sudo ufw delete allow 8080/tcp
```

Also remove the port from any cloud firewall (AWS Security Group, Hetzner Firewall, etc.).

---


## Multi-user tenancy and shared domains

Vector uses a deliberately small permission model:

- There is exactly one system administrator. The administrator manages accounts, not domain sharing.
- Every domain has exactly one owner. The owner supplies and controls that domain's encrypted Cloudflare token.
- The owner can grant or remove access for existing active users from the domain's **Manage access** panel.
- A shared member can create links on the domain and select it as their own default domain. They cannot read or replace the owner's token, verify/delete the domain, or manage its members.
- Removing access prevents future link creation on that domain but does not break links the member already created.
- Deactivating a user blocks login and revokes all sessions, while preserving links, domains, DNS state, and analytics so public redirects continue working.
- Domain deletion is blocked while any user's links still use the domain.

Switching an existing multi-user installation to single-user mode blocks regular-user login without deleting their data. Switching back to multi-user restores account eligibility, except for accounts explicitly deactivated by the administrator.

---

## Post-installation checklist

Run these commands to confirm everything is healthy:

```bash
# Check service status
sudo systemctl status vector vector-helper.socket nginx --no-pager

# Confirm Vector is only listening on loopback (not publicly)
sudo ss -lntp | grep 8081

# Test the HTTPS endpoint
curl -fsS https://YOUR_DOMAIN/healthz

# Dry-run certificate renewal
sudo certbot renew --dry-run

# Verify Nginx config is valid
sudo nginx -t
```

---

## Enable country analytics

Vector resolves visitor countries at zero latency using Cloudflare's `CF-IPCountry` header, which is validated against Cloudflare's official IP ranges — clients cannot spoof it.

**To enable Cloudflare country headers:**
1. In the Cloudflare dashboard, go to your domain → **Network**
2. Enable **IP Geolocation**, or go to **Rules → Transform Rules → Managed Transforms** and enable **Add visitor location headers**

**Optional: IPinfo Lite fallback** (for direct traffic not going through Cloudflare)
1. Get a free API token at [ipinfo.io](https://ipinfo.io)
2. In Vector, go to **Settings → Country Analytics** and paste the token
3. The token is validated, then encrypted with your master key — it is never returned by the API

---

## Backups

Back up both the database and the master key — **they must be backed up together**. A database without the master key cannot decrypt stored Cloudflare tokens.

```bash
# Stop Vector briefly for a clean backup (or use SQLite online backup)
sudo systemctl stop vector

sudo tar -C / -czf vector-backup-$(date +%F).tar.gz \
  opt/vector/data \
  etc/vector/master.key

sudo systemctl start vector

# Move the backup off-host and encrypt it
gpg --symmetric vector-backup-$(date +%F).tar.gz
```

> **Never commit `master.key` or the database to version control.**

---

## Updating

```bash
# Download and verify the new release bundle
wget https://github.com/SWARDAKSH777/vector/releases/download/v6.0.0-rc14/vector-release.tar.gz
tar -xzf vector-release.tar.gz
cd vector-*

# Run the preserving installer/upgrade (keeps your database, master.key, and config)
sudo bash install.sh
```

The installer verifies checksums before modifying anything and creates a rollback backup automatically.

---

## Security architecture

| Concern | Approach |
|---|---|
| Password hashing | Argon2id (t=3, m=64 MiB, p=2); existing PBKDF2/bcrypt hashes auto-migrate on login |
| Token encryption | AES-256-GCM with a root-managed external master key |
| Sessions | Server-side tokens, `__Host-` cookies, HttpOnly/Secure/SameSite=Strict, 24 h absolute / 2 h idle expiry |
| CSRF | HMAC session-bound tokens on all state-changing requests |
| Login throttling | Per-IP and per-account rate limits; timing-safe dummy hash prevents user enumeration |
| SQL injection | Parameterised queries throughout; SQLite `trusted_schema=OFF` |
| Proxy trust | Nginx injects a shared secret; Vector rejects forwarded headers from untrusted sources |
| Privileged operations | Socket-activated root helper; web process has zero sudo rights |
| Analytics privacy | Pseudonymous visitor IDs; no raw IP stored; referrer origin only; GPC/DNT honored |
| Tenant authorization | Creator-scoped links/analytics, membership-scoped domain use, owner-only token/DNS/domain administration, administrator-only account management |
| Dependency supply chain | Vendored `golang.org/x/crypto`; SQLite is the only CGO dependency; release checks include frontend audit and Go tests |

See [SECURITY.md](SECURITY.md), [THREAT_MODEL.md](THREAT_MODEL.md), and [PRIVACY.md](PRIVACY.md) for full details.

---

## Reporting a security issue

Please **do not open a public issue** with vulnerability details. Open a [private security advisory](../../security/advisories/new) on GitHub or use the contact in the Security tab. See [SECURITY.md](SECURITY.md) for response targets.

---

## License

MIT — see [LICENSE](LICENSE).

---

## What's not included

MFA/WebAuthn, SAML/OIDC/SSO, granular/custom RBAC, multiple administrators, high availability, external KMS, distributed rate limiting, infrastructure-level tenant isolation, and formal compliance certification are outside the scope of this release.
