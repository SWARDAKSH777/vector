package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"text/template"
	"time"
)

const (
	helperSocketPath        = "/run/vector-helper.sock"
	nginxManagedDir         = "/etc/nginx/conf.d"
	certbotCredentialDir    = "/etc/letsencrypt/vector-credentials"
	helperRoundTripTimeout  = 15 * time.Minute
	certbotOperationTimeout = 12 * time.Minute
)

type helperRequest struct {
	Action          string `json:"action"`
	Domain          string `json:"domain"`
	Port            string `json:"port,omitempty"`
	Email           string `json:"email,omitempty"`
	CloudflareToken string `json:"cloudflare_token,omitempty"`
}

type helperResponse struct {
	OK    bool   `json:"ok"`
	Log   string `json:"log,omitempty"`
	Error string `json:"error,omitempty"`
}

var helperOperationMu sync.Mutex

type nginxData struct {
	Domain      string
	Port        string
	Wildcard    bool
	HostPattern string
	ProxyAuth   string
}

const nginxHTTPTemplate = `# Managed by Vector URL Shortener. Do not edit manually.
server {
    server_tokens off;
    listen 80;
    listen [::]:80;
    server_name {{.Domain}};
    if ($host !~* "^{{.HostPattern}}$") { return 444; }

    location ^~ /.well-known/acme-challenge/ {
        root /var/lib/vector-acme;
        default_type text/plain;
        try_files $uri =404;
    }

    location / {
        proxy_pass http://127.0.0.1:{{.Port}};
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $vector_client_ip;
        proxy_set_header X-Forwarded-For $vector_client_ip;
        proxy_set_header X-Vector-Cloudflare-Trusted $vector_from_cloudflare;
        proxy_set_header X-Vector-Country $vector_country_code;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Vector-Proxy-Auth "{{.ProxyAuth}}";
        proxy_connect_timeout 5s;
        proxy_read_timeout 30s;
        proxy_send_timeout 30s;
        proxy_buffering off;
        client_max_body_size 256k;
    }
}
`

const nginxSSLTemplate = `# Managed by Vector URL Shortener. Do not edit manually.
server {
    server_tokens off;
    listen 80;
    listen [::]:80;
    server_name {{.Domain}}{{if .Wildcard}} *.{{.Domain}}{{end}};
    if ($host !~* "^{{.HostPattern}}$") { return 444; }
    location ^~ /.well-known/acme-challenge/ {
        root /var/lib/vector-acme;
        default_type text/plain;
        try_files $uri =404;
    }
    location / { return 308 https://$host$request_uri; }
}

server {
    server_tokens off;
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name {{.Domain}}{{if .Wildcard}} *.{{.Domain}}{{end}};
    if ($host !~* "^{{.HostPattern}}$") { return 444; }

    ssl_certificate     /etc/letsencrypt/live/{{.Domain}}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/{{.Domain}}/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:VectorSSL:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;

    add_header Strict-Transport-Security "max-age=31536000" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;

    location / {
        proxy_pass http://127.0.0.1:{{.Port}};
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $vector_client_ip;
        proxy_set_header X-Forwarded-For $vector_client_ip;
        proxy_set_header X-Vector-Cloudflare-Trusted $vector_from_cloudflare;
        proxy_set_header X-Vector-Country $vector_country_code;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Vector-Proxy-Auth "{{.ProxyAuth}}";
        proxy_connect_timeout 5s;
        proxy_read_timeout 30s;
        proxy_send_timeout 30s;
        proxy_buffering off;
        client_max_body_size 256k;
    }
}
`

func provisionDomain(s *server, domain, port string) (string, error) {
	clean, err := normalizeHostname(domain)
	if err != nil {
		return "", err
	}
	token, err := s.getDomainToken(clean)
	if err != nil {
		return "", fmt.Errorf("load Cloudflare token: %w", err)
	}
	var email string
	if err := s.db.QueryRow(`SELECT email FROM users ORDER BY id LIMIT 1`).Scan(&email); err != nil {
		return "", fmt.Errorf("load certificate contact email: %w", err)
	}
	req := helperRequest{
		Action: "provision", Domain: clean, Port: port, Email: email,
		CloudflareToken: token,
	}
	resp, err := callHelper(req)
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return resp.Log, errors.New(resp.Error)
	}
	return resp.Log, nil
}

func removeNginxConfig(domain string) {
	clean, err := normalizeHostname(domain)
	if err != nil {
		return
	}
	_, _ = callHelper(helperRequest{Action: "remove", Domain: clean})
}

func callHelper(req helperRequest) (helperResponse, error) {
	var out helperResponse
	conn, err := net.DialTimeout("unix", getenv("VECTOR_HELPER_SOCKET", helperSocketPath), 5*time.Second)
	if err != nil {
		return out, fmt.Errorf("privileged helper unavailable: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(helperRoundTripTimeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return out, err
	}
	// The helper validates that exactly one JSON object was sent by reading to
	// EOF. Half-close only the request direction so the helper can observe EOF
	// while this client continues reading the response. Without this, both
	// processes wait for each other until the socket deadline and the caller
	// receives an EOF instead of the helper result.
	if unixConn, ok := conn.(*net.UnixConn); ok {
		if err := unixConn.CloseWrite(); err != nil {
			return out, fmt.Errorf("finish privileged helper request: %w", err)
		}
	}
	if err := json.NewDecoder(io.LimitReader(bufio.NewReader(conn), 1<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("invalid helper response: %w", err)
	}
	return out, nil
}

func runPrivilegedHelper() error {
	if os.Geteuid() != 0 {
		return errors.New("helper must run as root")
	}
	allowedUID, allowedGID, err := helperServiceIDs()
	if err != nil {
		return err
	}
	socket := getenv("VECTOR_HELPER_SOCKET", helperSocketPath)
	ln, socketActivated, err := privilegedHelperListener(socket)
	if err != nil {
		return err
	}
	defer ln.Close()
	if !socketActivated {
		if err := os.Chown(socket, int(allowedUID), int(allowedGID)); err != nil {
			return fmt.Errorf("set helper socket ownership: %w", err)
		}
	}

	// The privileged helper is used only when domains or certificates change.
	// Under systemd socket activation it exits after a short idle period, while
	// systemd keeps the Unix socket available and starts it again on demand. This
	// removes an otherwise permanent Go process from the steady-state RAM budget.
	idleTimeout := time.Duration(intEnvBounded("VECTOR_HELPER_IDLE_TIMEOUT_SECONDS", 30, 5, 3600)) * time.Second
	var active atomic.Int64
	var handlers sync.WaitGroup
	concurrency := make(chan struct{}, 4)
	defer handlers.Wait()

	for {
		if socketActivated {
			if unixListener, ok := ln.(*net.UnixListener); ok {
				_ = unixListener.SetDeadline(time.Now().Add(idleTimeout))
			}
		}
		conn, err := ln.Accept()
		if err != nil {
			if socketActivated {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					if active.Load() == 0 {
						return nil
					}
					continue
				}
			}
			return err
		}
		peerUID, authorized := helperPeerUID(conn, allowedUID)
		if !authorized {
			log.Printf("Vector helper rejected local peer uid=%d; expected uid=%d", peerUID, allowedUID)
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_ = json.NewEncoder(conn).Encode(helperResponse{Error: "unauthorized helper client"})
			_ = conn.Close()
			continue
		}
		select {
		case concurrency <- struct{}{}:
		default:
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_ = json.NewEncoder(conn).Encode(helperResponse{Error: "privileged helper is busy"})
			_ = conn.Close()
			continue
		}
		active.Add(1)
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer active.Add(-1)
			defer func() { <-concurrency }()
			handleHelperConnection(conn)
		}()
	}
}

func privilegedHelperListener(socket string) (net.Listener, bool, error) {
	listenPID, pidErr := strconv.Atoi(strings.TrimSpace(os.Getenv("LISTEN_PID")))
	listenFDs, fdsErr := strconv.Atoi(strings.TrimSpace(os.Getenv("LISTEN_FDS")))
	if pidErr == nil && fdsErr == nil && listenPID == os.Getpid() && listenFDs == 1 {
		file := os.NewFile(uintptr(3), "vector-helper.socket")
		if file == nil {
			return nil, false, errors.New("systemd socket activation supplied an invalid file descriptor")
		}
		ln, err := net.FileListener(file)
		_ = file.Close()
		if err != nil {
			return nil, false, fmt.Errorf("use systemd helper socket: %w", err)
		}
		if _, ok := ln.(*net.UnixListener); !ok {
			_ = ln.Close()
			return nil, false, errors.New("systemd helper socket is not a Unix stream socket")
		}
		return ln, true, nil
	}

	if err := os.MkdirAll(filepath.Dir(socket), 0o750); err != nil {
		return nil, false, err
	}
	if info, statErr := os.Lstat(socket); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
			return nil, false, fmt.Errorf("refusing to replace non-socket helper path %q", socket)
		}
		if err := os.Remove(socket); err != nil {
			return nil, false, err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, false, statErr
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, false, err
	}
	if err := os.Chmod(socket, 0o660); err != nil {
		_ = ln.Close()
		return nil, false, err
	}
	return ln, false, nil
}

func helperServiceUID() (uint32, error) {
	uid, _, err := helperServiceIDs()
	return uid, err
}

func helperServiceIDs() (uint32, uint32, error) {
	account, err := user.Lookup("vector")
	if err != nil {
		return 0, 0, fmt.Errorf("lookup vector service user: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid vector service uid: %w", err)
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid vector service gid: %w", err)
	}
	return uint32(uid), uint32(gid), nil
}

func helperPeerUID(conn net.Conn, allowedUID uint32) (uint32, bool) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, false
	}
	var credentials *syscall.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, controlErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || controlErr != nil || credentials == nil {
		return 0, false
	}
	return credentials.Uid, credentials.Uid == allowedUID
}

func helperPeerAuthorized(conn net.Conn, allowedUID uint32) bool {
	_, ok := helperPeerUID(conn, allowedUID)
	return ok
}

func handleHelperConnection(conn net.Conn) {
	defer conn.Close()
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("Vector helper recovered from panic: %v", recovered)
			_ = json.NewEncoder(conn).Encode(helperResponse{Error: "privileged helper failed unexpectedly"})
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(helperRoundTripTimeout))
	dec := json.NewDecoder(ioLimitReader(conn, 32<<10))
	dec.DisallowUnknownFields()
	var req helperRequest
	if err := dec.Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(helperResponse{Error: "invalid helper request"})
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		_ = json.NewEncoder(conn).Encode(helperResponse{Error: "helper accepts exactly one request object"})
		return
	}
	resp := executeHelperRequest(req)
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		log.Printf("Vector helper could not return response: %v", err)
	}
}

func executeHelperRequest(req helperRequest) helperResponse {
	helperOperationMu.Lock()
	defer helperOperationMu.Unlock()
	domain, err := normalizeHostname(req.Domain)
	if err != nil {
		return helperResponse{Error: err.Error()}
	}
	switch req.Action {
	case "provision":
		if !validInternalPort(req.Port) {
			return helperResponse{Error: "invalid internal port"}
		}
		email, err := normalizeEmail(req.Email)
		if err != nil {
			return helperResponse{Error: "invalid certificate contact email"}
		}
		token, err := normalizeCloudflareToken(req.CloudflareToken)
		if err != nil {
			return helperResponse{Error: "invalid Cloudflare token"}
		}
		logText, err := helperProvision(domain, req.Port, email, token)
		if err != nil {
			return helperResponse{Log: logText, Error: err.Error()}
		}
		return helperResponse{OK: true, Log: logText}
	case "remove":
		if err := helperRemove(domain); err != nil {
			return helperResponse{Error: err.Error()}
		}
		return helperResponse{OK: true}
	default:
		return helperResponse{Error: "unsupported helper action"}
	}
}

func validInternalPort(port string) bool {
	allowed := getenv("VECTOR_INTERNAL_PORT", "8081")
	if port != allowed || len(port) > 5 {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1024 && n <= 65535
}

func certificateOwnershipPath(domain string) string {
	return filepath.Join(certbotCredentialDir, domain+".owner")
}

func certificateMaterialExists(domain string) bool {
	paths := []string{
		filepath.Join("/etc/letsencrypt/live", domain, "fullchain.pem"),
		filepath.Join("/etc/letsencrypt/renewal", domain+".conf"),
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func writeCertificateOwnership(domain string) (bool, error) {
	if err := rejectSymlinkDirectory(certbotCredentialDir, 0o700); err != nil {
		return false, err
	}
	path := certificateOwnershipPath(domain)
	if _, exists, err := validateManagedRegularFile(path, []byte("Managed by Vector URL Shortener")); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}
	tmp, err := os.CreateTemp(certbotCredentialDir, ".vector-owner-*.tmp")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.WriteString("Managed by Vector URL Shortener\n"); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, err
	}
	return true, nil
}

func helperProvision(domain, port, email, token string) (string, error) {
	var logs strings.Builder
	proxyAuth, err := helperProxySecret()
	if err != nil {
		return "", fmt.Errorf("load reverse-proxy authentication key: %w", err)
	}
	logLine := func(v string) { logs.WriteString(v + "\n") }
	if certificateMaterialExists(domain) {
		if _, err := os.Stat(certificateOwnershipPath(domain)); err != nil {
			return logs.String(), errors.New("refusing to adopt a pre-existing certificate not marked as Vector-managed")
		}
	}
	ownerCreated, err := writeCertificateOwnership(domain)
	if err != nil {
		return logs.String(), fmt.Errorf("could not record certificate ownership: %w", err)
	}
	cleanupOwnerOnFailure := true
	defer func() {
		if cleanupOwnerOnFailure && ownerCreated && !certificateMaterialExists(domain) {
			_ = os.Remove(certificateOwnershipPath(domain))
		}
	}()
	if err := os.MkdirAll("/var/lib/vector-acme/.well-known/acme-challenge", 0o755); err != nil {
		return logs.String(), err
	}
	confPath := filepath.Join(nginxManagedDir, "vector-"+domain+".conf")
	if err := nginxNameConflicts(domain, token != "", confPath); err != nil {
		return logs.String(), err
	}
	if err := renderAndInstallNginx(confPath, nginxHTTPTemplate, nginxData{Domain: domain, Port: port, HostPattern: nginxHostPattern(domain, false), ProxyAuth: proxyAuth}); err != nil {
		return logs.String(), err
	}
	logLine("✓ HTTP reverse proxy installed")

	args := []string{"certonly", "--non-interactive", "--agree-tos", "--no-eff-email", "--email", email, "--cert-name", domain, "-d", domain}
	if _, err := os.Stat(filepath.Join("/etc/letsencrypt/live", domain, "fullchain.pem")); err == nil {
		args = append(args, "--expand")
	}
	var credentials string
	if token != "" {
		var err error
		credentials, err = writeCertbotCredential(domain, token)
		if err != nil {
			return logs.String(), err
		}
		args = append(args, "--dns-cloudflare", "--dns-cloudflare-credentials", credentials,
			"--dns-cloudflare-propagation-seconds", "30", "-d", "*."+domain)
		logLine("✓ requesting exact and wildcard certificate with Cloudflare DNS-01")
	} else {
		args = append(args, "--webroot", "--webroot-path", "/var/lib/vector-acme")
		logLine("✓ requesting exact certificate with HTTP-01")
	}
	ctx, cancel := context.WithTimeout(context.Background(), certbotOperationTimeout)
	defer cancel()
	out, err := rootCommandContext(ctx, "/usr/bin/certbot", args...).CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return logs.String(), fmt.Errorf("certificate request exceeded %s; check Cloudflare DNS propagation and Certbot logs", certbotOperationTimeout)
	}
	if err != nil {
		return logs.String(), fmt.Errorf("certificate request failed: %s", sanitizeCommandOutput(out))
	}
	logLine("✓ TLS certificate obtained")
	if err := renderAndInstallNginx(confPath, nginxSSLTemplate, nginxData{Domain: domain, Port: port, Wildcard: token != "", HostPattern: nginxHostPattern(domain, token != ""), ProxyAuth: proxyAuth}); err != nil {
		return logs.String(), fmt.Errorf("certificate obtained but TLS proxy could not be installed: %w", err)
	}
	_ = os.Remove(filepath.Join(nginxManagedDir, "vector-bootstrap.conf"))
	if err := testAndReloadNginx(); err != nil {
		return logs.String(), err
	}
	logLine("✓ HTTPS reverse proxy active")
	cleanupOwnerOnFailure = false
	return logs.String(), nil
}

func helperRemove(domain string) error {
	path := filepath.Join(nginxManagedDir, "vector-"+domain+".conf")
	ownerPath := certificateOwnershipPath(domain)
	_, ownerErr := os.Stat(ownerPath)
	owned := ownerErr == nil
	if ownerErr != nil && !errors.Is(ownerErr, os.ErrNotExist) {
		return fmt.Errorf("could not inspect certificate ownership: %w", ownerErr)
	}

	old, configExists, configErr := validateManagedRegularFile(path, []byte("Managed by Vector URL Shortener"))
	if configErr != nil {
		return fmt.Errorf("could not inspect managed nginx config: %w", configErr)
	}
	if !configExists && !owned {
		// Nothing proves that a same-named certificate belongs to Vector.
		return nil
	}

	if configExists {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := testAndReloadNginx(); err != nil {
			_ = os.WriteFile(path, old, 0o600)
			_ = testAndReloadNginx()
			return err
		}
	}

	if owned && certificateMaterialExists(domain) {
		out, err := safeRootCommand("/usr/bin/certbot", "delete", "--cert-name", domain, "--non-interactive").CombinedOutput()
		if err != nil {
			return fmt.Errorf("certificate cleanup failed and can be retried safely: %s", sanitizeCommandOutput(out))
		}
	}
	_ = os.Remove(filepath.Join(certbotCredentialDir, domain+".ini"))
	_ = os.Remove(ownerPath)
	return nil
}

func writeCertbotCredential(domain, token string) (string, error) {
	if err := rejectSymlinkDirectory(certbotCredentialDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(certbotCredentialDir, domain+".ini")
	tmp, err := os.CreateTemp(certbotCredentialDir, ".vector-credential-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString("dns_cloudflare_api_token = " + token + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

func nginxHostPattern(domain string, wildcard bool) string {
	escaped := strings.ReplaceAll(domain, ".", `\.`)
	if wildcard {
		return `(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)?` + escaped
	}
	return escaped
}

func renderAndInstallNginx(path, tmplText string, data nginxData) error {
	tmpl, err := template.New("vector-nginx").Parse(tmplText)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	old, oldExists, oldErr := validateManagedRegularFile(path, []byte("Managed by Vector URL Shortener"))
	if oldErr != nil {
		return oldErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vector-nginx-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if err := testAndReloadNginx(); err != nil {
		if oldExists {
			_ = os.WriteFile(path, old, 0o600)
		} else {
			_ = os.Remove(path)
		}
		_ = testAndReloadNginx()
		return err
	}
	return nil
}

func rootCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = safeRootCommand(name).Env
	return cmd
}

func testAndReloadNginx() error {
	out, err := safeRootCommand("/usr/sbin/nginx", "-t").CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx validation failed: %s", sanitizeCommandOutput(out))
	}
	out, err = safeRootCommand("/usr/bin/systemctl", "reload", "nginx.service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx reload failed: %s", sanitizeCommandOutput(out))
	}
	return nil
}

func sanitizeCommandOutput(out []byte) string {
	text := strings.TrimSpace(string(out))
	if len(text) > 4096 {
		text = text[len(text)-4096:]
	}
	return text
}

// Small local reader wrapper avoids another dependency and caps helper requests.
type limitReader struct {
	r net.Conn
	n int64
}

func ioLimitReader(r net.Conn, n int64) *limitReader { return &limitReader{r: r, n: n} }
func (r *limitReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("helper request too large")
	}
	if int64(len(p)) > r.n {
		p = p[:r.n]
	}
	n, err := r.r.Read(p)
	r.n -= int64(n)
	return n, err
}
