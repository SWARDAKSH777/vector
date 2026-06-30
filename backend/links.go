package main

import (
	"crypto/rand"
	"database/sql"
	"strings"
	"time"
)

const (
	slugCodeAlphabet          = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	subdomainCodeAlphabet     = "0123456789abcdefghijklmnopqrstuvwxyz"
	generatedShortCodeLength  = 7
	maxShortCodeGenerateTries = 16
)

var cryptoRandRead = rand.Read

// randomCode returns an unbiased cryptographically-random alphanumeric code.
// Slugs are case-sensitive, so upper- and lower-case characters intentionally
// remain distinct members of the alphabet.
func randomCode(n int) (string, error) {
	return randomCodeFromAlphabet(n, slugCodeAlphabet)
}

func randomCodeFromAlphabet(n int, alphabet string) (string, error) {
	if n <= 0 {
		return "", &simpleErr{"short-code length must be positive"}
	}
	out := make([]byte, n)
	buf := make([]byte, n*2)
	if len(alphabet) < 2 || len(alphabet) > 256 {
		return "", &simpleErr{"short-code alphabet is invalid"}
	}
	limit := 256 - (256 % len(alphabet))
	written := 0
	for written < n {
		if _, err := cryptoRandRead(buf); err != nil {
			return "", err
		}
		for _, value := range buf {
			if int(value) >= limit {
				continue
			}
			out[written] = alphabet[int(value)%len(alphabet)]
			written++
			if written == n {
				break
			}
		}
	}
	return string(out), nil
}

func generateUniqueCode(db *sql.DB, domain, redirectType string) (string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return "", &simpleErr{"a domain is required before generating a short code"}
	}
	if redirectType != "subdomain" {
		redirectType = "slug"
	}
	for attempt := 0; attempt < maxShortCodeGenerateTries; attempt++ {
		alphabet := slugCodeAlphabet
		if redirectType == "subdomain" {
			alphabet = subdomainCodeAlphabet
		}
		code, err := randomCodeFromAlphabet(generatedShortCodeLength, alphabet)
		if err != nil {
			return "", err
		}
		if reservedCodes[strings.ToLower(code)] {
			continue
		}
		var exists int
		if err := db.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM links
			WHERE domain=? COLLATE NOCASE
			  AND redirect_type=?
			  AND short_code=? COLLATE BINARY
		)`, domain, redirectType, code).Scan(&exists); err != nil {
			return "", err
		}
		if exists == 0 {
			return code, nil
		}
	}
	return "", &simpleErr{"could not generate a unique short code, try again"}
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

type Link struct {
	ID             int64      `json:"id"`
	ShortCode      string     `json:"short_code"`
	ShortURL       string     `json:"short_url"`
	DestinationURL string     `json:"destination_url"`
	Domain         string     `json:"domain"`
	RedirectType   string     `json:"redirect_type"` // slug | subdomain
	Tag            string     `json:"tag"`
	Notes          string     `json:"notes"`
	HasPassword    bool       `json:"has_password"`
	ExpiresAt      *time.Time `json:"expires_at"`
	MaxClicks      *int64     `json:"max_clicks"`
	UTMSource      string     `json:"utm_source"`
	UTMMedium      string     `json:"utm_medium"`
	UTMCampaign    string     `json:"utm_campaign"`
	Status         string     `json:"status"`
	ClickCount     int64      `json:"click_count"`
	CreatedAt      time.Time  `json:"created_at"`
}
