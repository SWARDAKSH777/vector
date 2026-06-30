#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

INSTALL_DIR=/opt/vector
DATA_DB="$INSTALL_DIR/data/vector.db"
PURGE_DEPENDENCIES=0
CLOSE_PORT=0
ASSUME_YES=0
PURGE_BACKUPS=0
DOMAINS=()
NGINX_FILES=()

log()  { printf '\033[1;36m→\033[0m %s\n' "$*"; }
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[1;33m!\033[0m %s\n' "$*" >&2; }
fail() { printf '  \033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'__USAGE__'
Usage: sudo ./vector-total-purge.sh [options]

Options:
  --domain HOSTNAME       Also remove Vector's certificate for HOSTNAME (repeatable)
  --close-port-8080       Remove common UFW/firewalld rules for the temporary setup port
  --purge-backups         Also remove Vector backup archives and backup timer/service
  --purge-dependencies    Also uninstall Nginx/Certbot and delete /etc/nginx (dangerous)
  --yes                   Skip the "PURGE VECTOR" confirmation
  -h, --help              Show this help

By default this removes Vector completely while preserving shared Nginx and
Certbot packages. It never deletes Cloudflare DNS records because doing so
requires the per-domain token and an authenticated application workflow.
__USAGE__
}

normalize_domain() {
  local d="${1,,}"
  d="${d%.}"
  [[ "$d" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ && "$d" == *.* ]] || fail "Invalid domain: $1"
  printf '%s' "$d"
}
add_domain() {
  local d
  d="$(normalize_domain "$1")" || return 0
  local existing
  for existing in "${DOMAINS[@]:-}"; do [[ "$existing" == "$d" ]] && return 0; done
  DOMAINS+=("$d")
}

while (($#)); do
  case "$1" in
    --domain) shift; (($#)) || fail "--domain requires a hostname"; add_domain "$1" ;;
    --close-port-8080) CLOSE_PORT=1 ;;
    --purge-backups) PURGE_BACKUPS=1 ;;
    --purge-dependencies) PURGE_DEPENDENCIES=1 ;;
    --yes) ASSUME_YES=1 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "Unknown option: $1" ;;
  esac
  shift
done
[[ ${EUID:-$(id -u)} -eq 0 ]] || fail "Run as root: sudo ./vector-total-purge.sh"

# Discover domains before deleting the database.
if [[ -f "$DATA_DB" ]]; then
  while IFS= read -r d; do [[ -n "$d" ]] && add_domain "$d"; done < <(
    python3 - "$DATA_DB" <<'__DISCOVER_DB__' 2>/dev/null || true
import sqlite3,sys
try:
    db=sqlite3.connect(f"file:{sys.argv[1]}?mode=ro",uri=True)
    try:
        for (d,) in db.execute("SELECT hostname FROM domains"): print(d)
    except sqlite3.Error: pass
    try:
        row=db.execute("SELECT value FROM config WHERE key='domain'").fetchone()
        if row and row[0]: print(row[0])
    except sqlite3.Error: pass
except Exception: pass
__DISCOVER_DB__
  )
fi

# Discover only files carrying Vector's ownership marker.
if [[ -d /etc/nginx ]]; then
  while IFS= read -r -d '' f; do
    if grep -q 'Managed by Vector URL Shortener' "$f" 2>/dev/null; then
      NGINX_FILES+=("$f")
      while IFS= read -r d; do
        d="${d#\*.}"
        [[ -n "$d" && "$d" != _ ]] && add_domain "$d"
      done < <(awk '$1=="server_name" {for(i=2;i<=NF;i++){gsub(/;/,"",$i); print $i}}' "$f" 2>/dev/null || true)
    fi
  done < <(find /etc/nginx/conf.d /etc/nginx/sites-available -type f -print0 2>/dev/null || true)
fi

printf '\n\033[1mVector total purge plan\033[0m\n\n'
printf 'This permanently deletes:\n'
printf '  • application, database, links, accounts, analytics and encrypted tokens\n'
printf '  • master encryption key and bootstrap credentials\n'
printf '  • Vector web/helper services, Nginx vhosts and system user\n'
printf '  • certificates for the discovered/explicit domains listed below\n'
if ((${#DOMAINS[@]})); then printf '\nDomains:\n'; printf '  • %s\n' "${DOMAINS[@]}"; else printf '\nDomains: none discovered\n'; fi
if ((PURGE_DEPENDENCIES)); then
  warn "--purge-dependencies removes shared Nginx/Certbot packages and /etc/nginx. Other websites may break."
fi
if ((!ASSUME_YES)); then
  read -r -p 'Type PURGE VECTOR to continue: ' confirm
  [[ "$confirm" == 'PURGE VECTOR' ]] || fail "Purge cancelled"
  if ((PURGE_DEPENDENCIES)); then
    read -r -p 'Type REMOVE NGINX to confirm dependency removal: ' confirm
    [[ "$confirm" == 'REMOVE NGINX' ]] || fail "Dependency purge cancelled"
  fi
fi

log "Stopping and removing Vector services"
systemctl disable --now vector.service vector-helper.service vector-helper.socket vector-backup.timer vector-backup.service vector-cloudflare-ips.timer vector-cloudflare-ips.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/vector.service /etc/systemd/system/vector-helper.service /etc/systemd/system/vector-helper.socket
rm -f /etc/systemd/system/vector-backup.service /etc/systemd/system/vector-backup.timer
rm -f /etc/systemd/system/vector-cloudflare-ips.service /etc/systemd/system/vector-cloudflare-ips.timer
rm -rf /etc/systemd/system/vector.service.d /etc/systemd/system/vector-helper.service.d /etc/systemd/system/vector-helper.socket.d
find /etc/systemd/system -maxdepth 3 -type l \( -name vector.service -o -name vector-helper.service -o -name vector-helper.socket -o -name vector-backup.service -o -name vector-backup.timer -o -name vector-cloudflare-ips.service -o -name vector-cloudflare-ips.timer \) -delete 2>/dev/null || true
systemctl daemon-reload
systemctl reset-failed vector.service vector-helper.service vector-helper.socket >/dev/null 2>&1 || true
ok "Services removed"

log "Removing Vector-owned Nginx configuration"
rm -f /etc/nginx/conf.d/vector-bootstrap.conf /etc/nginx/conf.d/vector-cloudflare-trust-map.conf
for f in "${NGINX_FILES[@]:-}"; do
  [[ -f "$f" ]] || continue
  grep -q 'Managed by Vector URL Shortener' "$f" 2>/dev/null && rm -f -- "$f"
done
if [[ -f /etc/nginx/conf.d/vector.conf ]] && grep -q '/opt/vector/nginx' /etc/nginx/conf.d/vector.conf; then rm -f /etc/nginx/conf.d/vector.conf; fi
if [[ -f /etc/nginx/nginx.conf ]]; then
  sed -i '\#^[[:space:]]*include /opt/vector/nginx/\*\.conf;[[:space:]]*$#d' /etc/nginx/nginx.conf
fi
find /etc/nginx/sites-enabled -xtype l -delete 2>/dev/null || true

log "Removing exact Vector certificate names"
for d in "${DOMAINS[@]:-}"; do
  command -v certbot >/dev/null 2>&1 && certbot delete --cert-name "$d" --non-interactive >/dev/null 2>&1 || true
  rm -rf -- "/etc/letsencrypt/live/$d" "/etc/letsencrypt/archive/$d"
  rm -f -- "/etc/letsencrypt/renewal/$d.conf"
  ok "$d"
done

log "Removing application data, secrets and service identity"
rm -rf /opt/vector /etc/vector /run/vector /run/vector-helper.sock /var/lib/vector-acme /etc/letsencrypt/vector-credentials
rm -f /etc/sudoers.d/vector /usr/local/sbin/vector-bootstrap-reset /usr/local/sbin/vector-backup /usr/local/sbin/vector-update-cloudflare-ips /etc/letsencrypt/renewal-hooks/deploy/vector-reload-nginx
if ((PURGE_BACKUPS)); then
  rm -rf /var/backups/vector
  rm -rf /root/vector-nginx-stale-* /root/vector-pre-purge-* /root/vector-backup-* 2>/dev/null || true
  ok "Vector backup archives removed"
fi
if id vector >/dev/null 2>&1; then
  log "Terminating remaining Vector processes"

  # Stop user-scoped processes gracefully first.
  pkill -TERM -u vector 2>/dev/null || true

  for _ in {1..20}; do
    pgrep -u vector >/dev/null 2>&1 || break
    sleep 0.25
  done

  # Forcefully terminate anything that ignored SIGTERM.
  pkill -KILL -u vector 2>/dev/null || true

  for _ in {1..20}; do
    pgrep -u vector >/dev/null 2>&1 || break
    sleep 0.25
  done

  if pgrep -u vector >/dev/null 2>&1; then
    fail "Vector-owned processes are still running; refusing to report a successful purge"
  fi

  userdel vector || fail "Could not remove the Vector system user"
fi

if getent group vector >/dev/null 2>&1; then
  groupdel vector || fail "Could not remove the Vector system group"
fi

id vector >/dev/null 2>&1   && fail "Vector system user still exists after purge"

getent group vector >/dev/null 2>&1   && fail "Vector system group still exists after purge"

ok "Application, master key, system user and system group removed"

if ((CLOSE_PORT)); then
  log "Closing common firewall rules for TCP 8080"
  if command -v ufw >/dev/null 2>&1; then
    ufw --force delete allow 8080/tcp >/dev/null 2>&1 || true
  fi
  if command -v firewall-cmd >/dev/null 2>&1; then
    firewall-cmd --permanent --remove-port=8080/tcp >/dev/null 2>&1 || true
    while IFS= read -r rule; do
      [[ -n "$rule" ]] || continue
      if [[ "$rule" == *'port port="8080"'* || "$rule" == *'port="8080"'* ]]; then
        firewall-cmd --permanent --remove-rich-rule="$rule" >/dev/null 2>&1 || true
      fi
    done < <(firewall-cmd --permanent --zone=public --list-rich-rules 2>/dev/null || true)
    firewall-cmd --reload >/dev/null 2>&1 || true
  fi
fi

if ((PURGE_DEPENDENCIES)); then
  log "Purging Nginx and Certbot dependencies"
  systemctl disable --now nginx.service >/dev/null 2>&1 || true
  apt-get purge -y nginx nginx-common nginx-core certbot python3-certbot-dns-cloudflare python3-certbot-nginx 2>/dev/null || true
  apt-get autoremove -y 2>/dev/null || true
  rm -rf /etc/nginx /etc/letsencrypt /var/lib/letsencrypt /var/log/letsencrypt
else
  if command -v nginx >/dev/null 2>&1; then
    if nginx -t; then
      systemctl is-active --quiet nginx.service && systemctl reload nginx.service || true
      ok "Remaining Nginx configuration is valid"
    else
      warn "Nginx remains invalid due to a non-Vector configuration; inspect: nginx -t"
    fi
  fi
fi

printf '\nVector has been fully purged. Cloudflare DNS records were intentionally left untouched.\n'
