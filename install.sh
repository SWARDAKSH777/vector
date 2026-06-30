#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 0027

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
INSTALL_DIR=/opt/vector
DATA_DIR="$INSTALL_DIR/data"
BINARY="$INSTALL_DIR/vector"
CONFIG_DIR=/etc/vector
MASTER_KEY_FILE="$CONFIG_DIR/master.key"
RUNTIME_ENV_FILE="$CONFIG_DIR/runtime.env"
PROXY_KEY_FILE="$CONFIG_DIR/proxy.key"
ACME_DIR=/var/lib/vector-acme
BOOTSTRAP_FILE="$DATA_DIR/bootstrap.conf"
WEB_SERVICE=/etc/systemd/system/vector.service
HELPER_SERVICE=/etc/systemd/system/vector-helper.service
HELPER_SOCKET=/etc/systemd/system/vector-helper.socket
BOOTSTRAP_NGINX=/etc/nginx/conf.d/vector-bootstrap.conf
CLOUDFLARE_TRUST_CONF=/etc/nginx/conf.d/vector-cloudflare-trust-map.conf
CLOUDFLARE_UPDATE_SCRIPT=/usr/local/sbin/vector-update-cloudflare-ips
CLOUDFLARE_UPDATE_SERVICE=/etc/systemd/system/vector-cloudflare-ips.service
CLOUDFLARE_UPDATE_TIMER=/etc/systemd/system/vector-cloudflare-ips.timer

log()  { printf '\033[1;36m→\033[0m %s\n' "$*"; }
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[1;33m!\033[0m %s\n' "$*" >&2; }
fail() { printf '  \033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

printf '\n  VECTOR — production installer\n\n'

verify_release_integrity() {
  local manifest="$SCRIPT_DIR/MANIFEST.sha256"
  local line expected rel actual checked=0
  log "Checking release file integrity"
  [[ -f "$manifest" && ! -L "$manifest" ]] || fail "MANIFEST.sha256 is missing or unsafe; refusing installation."
  if find "$SCRIPT_DIR" -type l -print -quit | grep -q .; then
    fail "Release contains symbolic links; refusing installation."
  fi
  if find "$SCRIPT_DIR" -mindepth 1 ! -type f ! -type d -print -quit | grep -q .; then
    fail "Release contains a non-regular filesystem entry; refusing installation."
  fi
  declare -A listed=()
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    expected="${line%%[[:space:]]*}"
    rel="${line#*  }"
    [[ "$rel" != "$line" && "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || fail "Malformed manifest entry: $line"
    [[ "$rel" != /* && "$rel" != *".."* && "$rel" != *$'\n'* ]] || fail "Unsafe manifest path: $rel"
    [[ -f "$SCRIPT_DIR/$rel" && ! -L "$SCRIPT_DIR/$rel" ]] || fail "Release file is missing or unsafe: $rel"
    actual="$(sha256sum -- "$SCRIPT_DIR/$rel" | awk '{print $1}')"
    [[ "${actual,,}" == "${expected,,}" ]] || fail "Checksum mismatch: $rel"
    listed["$rel"]=1
    checked=$((checked+1))
  done < "$manifest"
  (( checked > 0 )) || fail "Manifest contains no files."
  for required in install.sh vector-linux-amd64 packaging/systemd/vector.service packaging/systemd/vector-helper.service packaging/systemd/vector-helper.socket packaging/nginx/vector-cloudflare-trust-map.conf packaging/scripts/vector-update-cloudflare-ips packaging/systemd/vector-cloudflare-ips.service packaging/systemd/vector-cloudflare-ips.timer; do
    [[ -n ${listed[$required]:-} ]] || fail "Security-sensitive release file is not covered by the manifest: $required"
  done
  while IFS= read -r -d '' candidate; do
    rel="${candidate#"$SCRIPT_DIR"/}"
    [[ "$rel" == "MANIFEST.sha256" ]] && continue
    [[ -n ${listed[$rel]:-} ]] || fail "Release contains an unlisted file: $rel"
  done < <(find "$SCRIPT_DIR" -type f -print0)
  ok "All $checked release files match the manifest, with no unlisted files"
  warn "Also verify the separately published archive checksum before extracting this bundle."
}
verify_release_integrity
if [[ ${1:-} == "--verify-only" ]]; then
  exit 0
elif [[ -n ${1:-} ]]; then
  fail "Unknown option: $1"
fi
[[ ${EUID:-$(id -u)} -eq 0 ]] || fail "Run as root: sudo bash install.sh"

case "$(uname -m)" in
  x86_64|amd64) SOURCE_BINARY="$SCRIPT_DIR/vector-linux-amd64" ;;
  *) fail "This bundle supports Linux AMD64 only (detected: $(uname -m))." ;;
esac
[[ -f "$SOURCE_BINARY" ]] || fail "vector-linux-amd64 is missing next to install.sh"
release_info="$("$SOURCE_BINARY" --version 2>/dev/null || true)"
if [[ "$release_info" == *"unverified-local"* || "$release_info" == "Vector dev "* ]]; then
  warn "This binary identifies as an unverified local/development build: ${release_info:-unknown}."
elif [[ "$release_info" == *"-rc"* ]]; then
  warn "This binary identifies as a release candidate: ${release_info:-unknown}. It is pre-release software; verify SHA256SUMS and the published GitHub attestation before production use."
fi

log "Installing runtime dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -q --no-install-recommends \
  nginx certbot python3-certbot-dns-cloudflare python3 openssl ca-certificates libsqlite3-0 curl

log "Creating least-privilege identities and directories"
systemctl stop vector.service vector-helper.service vector-helper.socket vector-cloudflare-ips.timer vector-cloudflare-ips.service >/dev/null 2>&1 || true
rm -f /run/vector/helper.sock /run/vector-helper.sock
rmdir /run/vector >/dev/null 2>&1 || true
getent group vector >/dev/null || groupadd --system vector
id -u vector >/dev/null 2>&1 || useradd --system --gid vector --home-dir "$INSTALL_DIR" --shell /usr/sbin/nologin vector
install -d -o root   -g root   -m 0755 "$INSTALL_DIR"
install -d -o vector -g vector -m 0750 "$DATA_DIR"
chown -R vector:vector "$DATA_DIR"
chmod 0750 "$DATA_DIR"
install -d -o root   -g vector -m 0750 "$CONFIG_DIR"
install -d -o root   -g root   -m 0755 "$ACME_DIR/.well-known/acme-challenge"
install -d -o root   -g root   -m 0700 /etc/letsencrypt/vector-credentials
install -d -o root   -g root   -m 0755 /etc/letsencrypt/renewal-hooks/deploy
install -o root -g root -m 0755 "$SOURCE_BINARY" "$BINARY"
install -o root -g root -m 0644 "$SCRIPT_DIR/packaging/nginx/vector-cloudflare-trust-map.conf" "$CLOUDFLARE_TRUST_CONF"
install -o root -g root -m 0755 "$SCRIPT_DIR/packaging/scripts/vector-update-cloudflare-ips" "$CLOUDFLARE_UPDATE_SCRIPT"
install -o root -g root -m 0644 "$SCRIPT_DIR/packaging/systemd/vector-cloudflare-ips.service" "$CLOUDFLARE_UPDATE_SERVICE"
install -o root -g root -m 0644 "$SCRIPT_DIR/packaging/systemd/vector-cloudflare-ips.timer" "$CLOUDFLARE_UPDATE_TIMER"

# Upgrade proof: old Vector releases had marked Nginx files but no separate
# certificate-ownership marker. Create one only when both the managed file and
# its matching certificate already exist; never infer ownership from a name alone.
shopt -s nullglob
for cfg in /etc/nginx/conf.d/vector-*.conf; do
  grep -q 'Managed by Vector URL Shortener' "$cfg" || continue
  domain="$(basename "$cfg")"
  domain="${domain#vector-}"
  domain="${domain%.conf}"
  [[ "$domain" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]] || continue
  if [[ -f "/etc/letsencrypt/live/$domain/fullchain.pem" ]]; then
    printf 'Managed by Vector URL Shortener\n' > "/etc/letsencrypt/vector-credentials/$domain.owner"
    chown root:root "/etc/letsencrypt/vector-credentials/$domain.owner"
    chmod 0600 "/etc/letsencrypt/vector-credentials/$domain.owner"
  fi
done
shopt -u nullglob
cat > /etc/letsencrypt/renewal-hooks/deploy/vector-reload-nginx <<'__RENEW_HOOK__'
#!/usr/bin/env bash
set -Eeuo pipefail
/usr/sbin/nginx -t
/usr/bin/systemctl reload nginx.service
__RENEW_HOOK__
chmod 0750 /etc/letsencrypt/renewal-hooks/deploy/vector-reload-nginx
chown root:root /etc/letsencrypt/renewal-hooks/deploy/vector-reload-nginx

# Remove broad privilege mechanisms and runtime-writable Nginx paths from old releases.
rm -f /etc/sudoers.d/vector
rm -rf /opt/vector/nginx /etc/systemd/system/vector.service.d
if [[ -f /etc/nginx/conf.d/vector.conf ]] && grep -q '/opt/vector/nginx' /etc/nginx/conf.d/vector.conf; then
  rm -f /etc/nginx/conf.d/vector.conf
fi
if [[ -f /etc/nginx/nginx.conf ]]; then
  sed -i '\#^[[:space:]]*include /opt/vector/nginx/\*\.conf;[[:space:]]*$#d' /etc/nginx/nginx.conf
fi

if [[ ! -s "$MASTER_KEY_FILE" ]]; then
  log "Generating the external encryption/session master key"
  openssl rand 32 | base64 -w0 | tr -d '=' > "$MASTER_KEY_FILE"
  printf '\n' >> "$MASTER_KEY_FILE"
  chown root:vector "$MASTER_KEY_FILE"
  chmod 0640 "$MASTER_KEY_FILE"
  ok "Master key created at $MASTER_KEY_FILE"
  warn "Back up this key with vector.db. Losing it makes encrypted tokens unrecoverable."
else
  chown root:vector "$MASTER_KEY_FILE"
  chmod 0640 "$MASTER_KEY_FILE"
  ok "Existing master key preserved"
fi

if [[ ! -s "$PROXY_KEY_FILE" ]]; then
  log "Generating the Nginx-to-Vector authentication key"
  openssl rand -base64 48 | tr '+/' '-_' | tr -d '=\n' > "$PROXY_KEY_FILE"
  printf '\n' >> "$PROXY_KEY_FILE"
fi
chown root:vector "$PROXY_KEY_FILE"
chmod 0640 "$PROXY_KEY_FILE"
PROXY_AUTH_VALUE="$(tr -d '\r\n' < "$PROXY_KEY_FILE")"
[[ "$PROXY_AUTH_VALUE" =~ ^[A-Za-z0-9_-]{43,128}$ ]] || fail "Generated proxy authentication key is invalid"

# Non-secret runtime tuning is kept outside the application database. IPinfo
# credentials are managed from Settings and stored encrypted in vector.db.
if [[ ! -e "$RUNTIME_ENV_FILE" ]]; then
  cat > "$RUNTIME_ENV_FILE" <<'__VECTOR_RUNTIME_ENV__'
# Country-only analytics tuning. Add/replace/delete the IPinfo Lite token in
# Vector Settings; secrets are never stored in this environment file.
GEO_CACHE_TTL_HOURS=168
GEO_LOOKUP_WORKERS=1
GEO_LOOKUP_QUEUE_SIZE=256
GEO_MEMORY_CACHE_ENTRIES=512

# Low-memory runtime defaults. Increase only after measuring sustained load.
DB_MAX_OPEN_CONNS=2
DB_MAX_IDLE_CONNS=1
GOMAXPROCS=1
GOMEMLIMIT=64MiB
GOGC=50
GODEBUG=madvdontneed=1

__VECTOR_RUNTIME_ENV__
fi
python3 - "$RUNTIME_ENV_FILE" <<'__REMOVE_OBSOLETE_CF_SWITCH__'
import os, pathlib, sys, tempfile
path=pathlib.Path(sys.argv[1])
lines=path.read_text(encoding="utf-8").splitlines() if path.exists() else []
out=[line for line in lines if not line.startswith("TRUST_CLOUDFLARE_HEADERS=")]
fd,tmp=tempfile.mkstemp(prefix=".runtime.env.", dir=str(path.parent), text=True)
try:
    with os.fdopen(fd,"w",encoding="utf-8",newline="\n") as handle:
        handle.write("\n".join(out).rstrip("\n")+"\n")
        handle.flush(); os.fsync(handle.fileno())
    os.chmod(tmp,0o640); os.replace(tmp,path)
finally:
    try: os.unlink(tmp)
    except FileNotFoundError: pass
__REMOVE_OBSOLETE_CF_SWITCH__

# Persist only a validated public origin IP supplied by the trusted installer.
# This lets setup create the primary DNS record even when the administrator uses
# an SSH tunnel or the VPS has a private primary interface address.
if [[ -n ${VECTOR_PUBLIC_IP:-} ]]; then
  python3 - "$VECTOR_PUBLIC_IP" <<'__PUBLIC_IP_VALIDATE__' >/dev/null 2>&1 || fail "VECTOR_PUBLIC_IP is not a valid public IP address"
import ipaddress,sys
ip=ipaddress.ip_address(sys.argv[1])
if not ip.is_global:
    raise SystemExit(1)
__PUBLIC_IP_VALIDATE__
  python3 - "$RUNTIME_ENV_FILE" "VECTOR_PUBLIC_IP" "$VECTOR_PUBLIC_IP" <<'__RUNTIME_ENV_UPDATE__'
import os, pathlib, sys, tempfile
path=pathlib.Path(sys.argv[1])
key=sys.argv[2]
value=sys.argv[3]
if any(ch in value for ch in "\r\n\x00"):
    raise SystemExit("invalid runtime value")
lines=path.read_text(encoding="utf-8").splitlines() if path.exists() else []
out=[]
replaced=False
for line in lines:
    if line.startswith(key+"="):
        if not replaced:
            out.append(f"{key}={value}")
            replaced=True
        continue
    out.append(line)
if not replaced:
    if out and out[-1] != "":
        out.append("")
    out.append(f"{key}={value}")
fd,tmp=tempfile.mkstemp(prefix=".runtime.env.", dir=str(path.parent), text=True)
try:
    with os.fdopen(fd,"w",encoding="utf-8",newline="\n") as handle:
        handle.write("\n".join(out).rstrip("\n")+"\n")
        handle.flush()
        os.fsync(handle.fileno())
    os.chmod(tmp,0o640)
    os.replace(tmp,path)
finally:
    try: os.unlink(tmp)
    except FileNotFoundError: pass
__RUNTIME_ENV_UPDATE__
  ok "Configured public origin IP: $VECTOR_PUBLIC_IP"
fi
chown root:vector "$RUNTIME_ENV_FILE"
chmod 0640 "$RUNTIME_ENV_FILE"

setup_complete=false
if [[ -f "$DATA_DIR/vector.db" ]]; then
  setup_complete="$(python3 - "$DATA_DIR/vector.db" <<'__PY_DB__' 2>/dev/null || printf 'false'
import sqlite3, sys
try:
    db=sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
    row=db.execute("SELECT value FROM config WHERE key='setup_complete'").fetchone()
    print("true" if row and row[0] == "true" else "false")
except Exception:
    print("false")
__PY_DB__
)"
fi

make_bootstrap() {
  BOOTSTRAP_USERNAME="vector-bootstrap-$(openssl rand -hex 4)"
  BOOTSTRAP_PASSWORD="$(openssl rand -hex 24)"
  BOOTSTRAP_HASH="$(printf '%s' "$BOOTSTRAP_PASSWORD" | python3 -c '
import base64, hashlib, secrets, sys
password=sys.stdin.buffer.read()
salt=secrets.token_bytes(16)
h=hashlib.pbkdf2_hmac("sha256", password, salt, 600000, 32)
enc=lambda b: base64.b64encode(b).decode().rstrip("=")
print(f"pbkdf2-sha256$600000${enc(salt)}${enc(h)}")
')"
  cat > "$BOOTSTRAP_FILE" <<__BOOTSTRAP_CONFIG__
# Generated by Vector. The plaintext password is never stored.
version=2
username=$BOOTSTRAP_USERNAME
password_hash=$BOOTSTRAP_HASH
__BOOTSTRAP_CONFIG__
  chown root:vector "$BOOTSTRAP_FILE"
  chmod 0640 "$BOOTSTRAP_FILE"
}

BOOTSTRAP_CREATED=0
if [[ "$setup_complete" != true ]]; then
  make_bootstrap
  BOOTSTRAP_CREATED=1
else
  rm -f "$BOOTSTRAP_FILE"
fi

log "Installing the root-only bootstrap reset command"
cat > /usr/local/sbin/vector-bootstrap-reset <<'__RESET_SCRIPT__'
#!/usr/bin/env bash
set -Eeuo pipefail
umask 0027
[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo "Run as root: sudo vector-bootstrap-reset" >&2; exit 1; }
DATA_DIR=/opt/vector/data
DB="$DATA_DIR/vector.db"
FILE="$DATA_DIR/bootstrap.conf"
if [[ -f "$DB" ]]; then
  complete="$(python3 - "$DB" <<'__RESET_DB__' 2>/dev/null || printf 'false'
import sqlite3,sys
try:
    db=sqlite3.connect(f"file:{sys.argv[1]}?mode=ro",uri=True)
    row=db.execute("SELECT value FROM config WHERE key='setup_complete'").fetchone()
    print("true" if row and row[0]=="true" else "false")
except Exception: print("false")
__RESET_DB__
)"
  [[ "$complete" != true ]] || { echo "Setup is already complete; bootstrap reset is disabled." >&2; exit 1; }
fi
username="vector-bootstrap-$(openssl rand -hex 4)"
password="$(openssl rand -hex 24)"
hash="$(printf '%s' "$password" | python3 -c '
import base64,hashlib,secrets,sys
password=sys.stdin.buffer.read()
salt=secrets.token_bytes(16)
h=hashlib.pbkdf2_hmac("sha256",password,salt,600000,32)
e=lambda b:base64.b64encode(b).decode().rstrip("=")
print(f"pbkdf2-sha256$600000${e(salt)}${e(h)}")
')"
install -d -o vector -g vector -m 0750 "$DATA_DIR"
cat > "$FILE" <<__RESET_CONFIG__
# Generated by vector-bootstrap-reset. The plaintext password is never stored.
version=2
username=$username
password_hash=$hash
__RESET_CONFIG__
chown root:vector "$FILE"
chmod 0640 "$FILE"
systemctl restart vector.service >/dev/null 2>&1 || true
printf '\nVector secure setup credentials (shown once)\n  Username: %s\n  Password: %s\n\n' "$username" "$password"
__RESET_SCRIPT__
chown root:root /usr/local/sbin/vector-bootstrap-reset
chmod 0750 /usr/local/sbin/vector-bootstrap-reset

log "Installing hardened systemd units"
cat > "$HELPER_SOCKET" <<'__HELPER_SOCKET_UNIT__'
[Unit]
Description=Vector privileged helper socket
Before=vector.service

[Socket]
ListenStream=/run/vector-helper.sock
SocketUser=root
SocketGroup=vector
SocketMode=0660
DirectoryMode=0750
Backlog=16
MaxConnections=16
RemoveOnStop=true

[Install]
WantedBy=sockets.target
__HELPER_SOCKET_UNIT__

cat > "$HELPER_SERVICE" <<'__HELPER_UNIT__'
[Unit]
Description=Vector constrained privileged helper
Requires=vector-helper.socket
Wants=network-online.target nginx.service
After=network-online.target nginx.service vector-helper.socket

[Service]
Type=simple
User=root
Group=vector
Environment=PYTHONDONTWRITEBYTECODE=1
Environment=VECTOR_INTERNAL_PORT=8081
Environment=VECTOR_PROXY_KEY_FILE=/etc/vector/proxy.key
Environment=VECTOR_HELPER_IDLE_TIMEOUT_SECONDS=30
Environment=GOMAXPROCS=1
Environment=GOMEMLIMIT=32MiB
Environment=GOGC=50
Environment=GODEBUG=madvdontneed=1
UMask=0077
ExecStart=/opt/vector/vector helper
Restart=no
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectHostname=true
ProtectClock=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
ProtectProc=invisible
ProcSubset=pid
MemoryDenyWriteExecute=true
SystemCallArchitectures=native
RestrictSUIDSGID=true
RestrictNamespaces=true
LockPersonality=true
RestrictRealtime=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=/etc/nginx/conf.d /etc/letsencrypt -/var/lib/letsencrypt -/var/log/letsencrypt /var/lib/vector-acme -/run/nginx.pid -/var/log/nginx -/var/lib/nginx
__HELPER_UNIT__

cat > "$WEB_SERVICE" <<'__WEB_UNIT__'
[Unit]
Description=Vector URL Shortener
Requires=vector-helper.socket
Wants=network-online.target nginx.service
After=network-online.target nginx.service vector-helper.socket

[Service]
Type=simple
User=vector
Group=vector
UMask=0077
WorkingDirectory=/opt/vector
ExecStart=/opt/vector/vector
Environment=DATA_DIR=/opt/vector/data
Environment=VECTOR_MASTER_KEY_FILE=/etc/vector/master.key
Environment=VECTOR_HELPER_SOCKET=/run/vector-helper.sock
Environment=VECTOR_PROXY_KEY_FILE=/etc/vector/proxy.key
EnvironmentFile=-/etc/vector/runtime.env
Environment=LISTEN_ADDR=127.0.0.1:8081
Environment=INTERNAL_PORT=8081
Restart=on-failure
RestartSec=5s
TimeoutStopSec=20s
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectHostname=true
ProtectClock=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
ProtectProc=invisible
ProcSubset=pid
RestrictSUIDSGID=true
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictRealtime=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=/opt/vector/data

[Install]
WantedBy=multi-user.target
__WEB_UNIT__

log "Preparing Nginx"
# Quarantine only stale files explicitly marked as Vector-managed and referring
# to a missing certificate. Unrelated Nginx sites are never changed.
backup_dir="/root/vector-nginx-stale-$(date +%Y%m%d-%H%M%S)"
while IFS= read -r -d '' cfg; do
  [[ -f "$cfg" ]] || continue
  grep -q 'Managed by Vector URL Shortener' "$cfg" || continue
  missing=0
  while IFS= read -r cert; do
    [[ -z "$cert" ]] && continue
    [[ -e "$cert" ]] || missing=1
  done < <(awk '$1=="ssl_certificate" || $1=="ssl_certificate_key" {gsub(/;/,"",$2); print $2}' "$cfg")
  if (( missing )); then
    rel="${cfg#/}"
    mkdir -p "$backup_dir/$(dirname "$rel")"
    cp -a "$cfg" "$backup_dir/$rel"
    rm -f "$cfg"
    warn "Quarantined stale Vector Nginx file: $cfg"
  fi
done < <(find /etc/nginx/conf.d /etc/nginx/sites-available -type f -print0 2>/dev/null || true)
find /etc/nginx/sites-enabled -xtype l -delete 2>/dev/null || true

if [[ "$setup_complete" != true ]]; then
  cat > "$BOOTSTRAP_NGINX" <<'__BOOTSTRAP_NGINX__'
# Managed by Vector URL Shortener. Temporary bootstrap listener; removed after TLS succeeds.
server {
    listen 8080 default_server;
    listen [::]:8080 default_server;
    server_name _;
    access_log off;
    client_max_body_size 256k;
    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $vector_client_ip;
        proxy_set_header X-Forwarded-For $vector_client_ip;
        proxy_set_header X-Vector-Cloudflare-Trusted $vector_from_cloudflare;
        proxy_set_header X-Vector-Country $vector_country_code;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Vector-Proxy-Auth "__VECTOR_PROXY_AUTH__";
        proxy_connect_timeout 5s;
        # DNS-01 certificate provisioning can take several minutes.
        proxy_read_timeout 720s;
        proxy_send_timeout 720s;
        proxy_buffering off;
    }
}
__BOOTSTRAP_NGINX__
  python3 - "$BOOTSTRAP_NGINX" "$PROXY_AUTH_VALUE" <<'__INJECT_PROXY_KEY__'
import os,pathlib,sys,tempfile
path=pathlib.Path(sys.argv[1]); secret=sys.argv[2]
data=path.read_text(encoding="utf-8").replace("__VECTOR_PROXY_AUTH__",secret)
fd,tmp=tempfile.mkstemp(prefix=".vector-nginx.",dir=str(path.parent),text=True)
try:
    with os.fdopen(fd,"w",encoding="utf-8",newline="\n") as f:
        f.write(data); f.flush(); os.fsync(f.fileno())
    os.chmod(tmp,0o600); os.replace(tmp,path)
finally:
    try: os.unlink(tmp)
    except FileNotFoundError: pass
__INJECT_PROXY_KEY__
else
  rm -f "$BOOTSTRAP_NGINX"
fi

python3 - "$PROXY_AUTH_VALUE" <<'__PATCH_VECTOR_NGINX__'
import os,pathlib,re,sys,tempfile
secret=sys.argv[1]
for path in pathlib.Path("/etc/nginx/conf.d").glob("vector-*.conf"):
    if path.name == "vector-cloudflare-trust-map.conf":
        continue
    if path.is_symlink() or not path.is_file():
        raise SystemExit(f"unsafe Vector nginx path: {path}")
    text=path.read_text(encoding="utf-8")
    if "Managed by Vector URL Shortener" not in text:
        continue
    desired={
        "X-Real-IP": "        proxy_set_header X-Real-IP $vector_client_ip;",
        "X-Forwarded-For": "        proxy_set_header X-Forwarded-For $vector_client_ip;",
        "X-Vector-Cloudflare-Trusted": "        proxy_set_header X-Vector-Cloudflare-Trusted $vector_from_cloudflare;",
        "X-Vector-Country": "        proxy_set_header X-Vector-Country $vector_country_code;",
        "X-Vector-Proxy-Auth": f'        proxy_set_header X-Vector-Proxy-Auth "{secret}";',
    }
    for name,line in desired.items():
        pattern=rf'^[ \t]*proxy_set_header {re.escape(name)}[^;]*;[ \t]*$'
        if re.search(pattern,text,flags=re.M):
            text=re.sub(pattern,line,text,flags=re.M)
        else:
            for marker in (
                "        proxy_set_header X-Forwarded-Host $http_host;",
                "        proxy_set_header X-Forwarded-Host $host;",
            ):
                if marker in text:
                    text = text.replace(marker, marker+"\n"+line, 1)
                    break
    fd,tmp=tempfile.mkstemp(prefix=".vector-nginx.",dir=str(path.parent),text=True)
    try:
        with os.fdopen(fd,"w",encoding="utf-8",newline="\n") as f:
            f.write(text); f.flush(); os.fsync(f.fileno())
        os.chmod(tmp,0o600); os.replace(tmp,path)
    finally:
        try: os.unlink(tmp)
        except FileNotFoundError: pass
__PATCH_VECTOR_NGINX__

nginx -t || fail "Nginx configuration is invalid; Vector services were not started."
systemctl enable nginx.service >/dev/null 2>&1 || true
if systemctl is-active --quiet nginx.service; then
  systemctl reload nginx.service || fail "Nginx configuration is valid but could not be reloaded."
else
  systemctl start nginx.service || fail "Nginx configuration is valid but Nginx could not be started."
fi
if systemctl list-unit-files certbot.timer >/dev/null 2>&1; then
  systemctl enable --now certbot.timer >/dev/null 2>&1 || warn "Could not enable certbot.timer; configure certificate renewal monitoring manually."
fi
systemctl daemon-reload
systemctl enable --now vector-cloudflare-ips.timer >/dev/null 2>&1 || warn "Could not enable the automatic Cloudflare IP-range refresh timer"
systemctl disable vector-helper.service >/dev/null 2>&1 || true
systemctl enable --now vector-helper.socket vector.service

log "Running local health checks"
for _ in {1..20}; do
  curl -fsS --max-time 2 http://127.0.0.1:8081/healthz >/dev/null 2>&1 && break
  sleep 0.5
done
if ! curl -fsS --max-time 3 http://127.0.0.1:8081/healthz >/dev/null; then
  journalctl -u vector.service -n 80 --no-pager >&2 || true
  fail "Vector failed its local health check"
fi
# Older releases stored IPINFO_TOKEN in this root-managed file. The first
# successful start above migrates it into encrypted database storage. Remove the
# plaintext environment copy and restart so Settings becomes the only source.
if grep -q '^IPINFO_TOKEN=' "$RUNTIME_ENV_FILE" 2>/dev/null; then
  python3 - "$RUNTIME_ENV_FILE" <<'__REMOVE_LEGACY_IPINFO__'
import os, pathlib, sys, tempfile
path=pathlib.Path(sys.argv[1])
lines=path.read_text(encoding="utf-8").splitlines() if path.exists() else []
out=[line for line in lines if not line.startswith("IPINFO_TOKEN=")]
fd,tmp=tempfile.mkstemp(prefix=".runtime.env.", dir=str(path.parent), text=True)
try:
    with os.fdopen(fd,"w",encoding="utf-8",newline="\n") as handle:
        handle.write("\n".join(out).rstrip("\n")+"\n")
        handle.flush()
        os.fsync(handle.fileno())
    os.chmod(tmp,0o640)
    os.replace(tmp,path)
finally:
    try: os.unlink(tmp)
    except FileNotFoundError: pass
__REMOVE_LEGACY_IPINFO__
  chown root:vector "$RUNTIME_ENV_FILE"
  chmod 0640 "$RUNTIME_ENV_FILE"
  systemctl restart vector.service
  for _ in {1..20}; do
    curl -fsS --max-time 2 http://127.0.0.1:8081/healthz >/dev/null 2>&1 && break
    sleep 0.5
  done
  curl -fsS --max-time 3 http://127.0.0.1:8081/healthz >/dev/null || fail "Vector failed after migrating the legacy IPinfo token"
  ok "Migrated the legacy IPinfo token into encrypted Settings storage"
fi

ok "Vector is running and its constrained helper socket is ready"

server_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
[[ -n "$server_ip" ]] || server_ip=SERVER_IP
printf '\nInstallation complete.\n'
if (( BOOTSTRAP_CREATED )); then
  printf '\nSECURE SETUP CREDENTIALS — SHOWN ONCE\n\n  Username: %s\n  Password: %s\n\n' "$BOOTSTRAP_USERNAME" "$BOOTSTRAP_PASSWORD"
  printf 'Open: http://%s:8080/setup\n' "$server_ip"
  printf 'Keep TCP 8080 open only until HTTPS setup succeeds; Vector removes the temporary listener afterward.\n'
else
  printf 'Existing completed setup preserved.\n'
fi
printf '\nServices: systemctl status vector vector-helper.socket nginx\n'
