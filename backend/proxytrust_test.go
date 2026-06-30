package main

import "net/http"

const testProxySecret = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"

func init() {
	trustedProxySecret.Store([]byte(testProxySecret))
}

func markTrustedProxy(r *http.Request) {
	r.Header.Set(proxyAuthHeader, testProxySecret)
}
