package main

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
)

const proxyAuthHeader = "X-Vector-Proxy-Auth"

var trustedProxySecret atomic.Value // stores []byte

func configureTrustedProxySecret() error {
	path := strings.TrimSpace(getenv("VECTOR_PROXY_KEY_FILE", "/etc/vector/proxy.key"))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	secret := strings.TrimSpace(string(data))
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) < 32 {
		return errors.New("proxy authentication key is malformed")
	}
	trustedProxySecret.Store([]byte(secret))
	return nil
}

func setTrustedProxySecretForTest(secret string) func() {
	old, _ := trustedProxySecret.Load().([]byte)
	trustedProxySecret.Store([]byte(secret))
	return func() { trustedProxySecret.Store(old) }
}

func trustedProxyRequest(r *http.Request) bool {
	if r == nil || !isLoopbackRemote(r.RemoteAddr) {
		return false
	}
	want, _ := trustedProxySecret.Load().([]byte)
	got := strings.TrimSpace(r.Header.Get(proxyAuthHeader))
	return len(want) >= 32 && len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), want) == 1
}

func helperProxySecret() (string, error) {
	path := strings.TrimSpace(getenv("VECTOR_PROXY_KEY_FILE", "/etc/vector/proxy.key"))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(data))
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) < 32 {
		return "", errors.New("proxy authentication key is malformed")
	}
	return secret, nil
}
