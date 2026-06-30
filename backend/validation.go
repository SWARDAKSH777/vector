package main

import (
	"errors"
	"net/mail"
	"strings"
)

func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) == 0 || len(email) > 254 {
		return "", errors.New("invalid email address")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(addr.Address, email) || !strings.Contains(email, "@") {
		return "", errors.New("invalid email address")
	}
	return email, nil
}

func trimAndLimit(value string, max int, field string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return "", &simpleErr{field + " is too long"}
	}
	return value, nil
}

func normalizeCloudflareToken(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", nil
	}
	if len(token) > 512 {
		return "", errors.New("Cloudflare API token is too long")
	}
	for _, r := range token {
		if r < 0x21 || r == 0x7f {
			return "", errors.New("Cloudflare API token contains invalid whitespace or control characters")
		}
	}
	return token, nil
}
