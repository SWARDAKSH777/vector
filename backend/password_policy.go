package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	minimumAdminPasswordRunes = 15
	maximumPasswordBytes      = 1024
	minimumLinkPasswordRunes  = 10
)

// A deliberately small, high-confidence blocklist catches the most common
// choices without shipping a large password corpus in the binary. Deployments
// that require a broader breach corpus should add an external compromised-
// password service at the identity layer.
var commonPasswords = map[string]struct{}{
	"123456": {}, "12345678": {}, "123456789": {}, "1234567890": {},
	"password": {}, "password1": {}, "password123": {}, "admin": {},
	"admin123": {}, "administrator": {}, "qwerty": {}, "qwerty123": {},
	"letmein": {}, "welcome": {}, "welcome123": {}, "iloveyou": {},
	"monkey": {}, "dragon": {}, "football": {}, "abc123": {},
	"111111": {}, "000000": {}, "passw0rd": {}, "vector": {},
	"vector123": {}, "changeme": {}, "default": {}, "root": {},
}

func validateAdminPassword(password string) error {
	if len(password) > maximumPasswordBytes {
		return fmt.Errorf("password is too long (maximum %d bytes)", maximumPasswordBytes)
	}
	if !utf8.ValidString(password) {
		return fmt.Errorf("password must be valid UTF-8")
	}
	if utf8.RuneCountInString(password) < minimumAdminPasswordRunes {
		return fmt.Errorf("password must be at least %d characters", minimumAdminPasswordRunes)
	}
	if _, blocked := commonPasswords[strings.ToLower(password)]; blocked {
		return fmt.Errorf("choose a less common password or passphrase")
	}
	return nil
}

func validateLinkPassword(password string) error {
	if password == "" {
		return nil
	}
	if len(password) > maximumPasswordBytes || !utf8.ValidString(password) {
		return fmt.Errorf("link password is invalid")
	}
	if utf8.RuneCountInString(password) < minimumLinkPasswordRunes {
		return fmt.Errorf("link password must be at least %d characters", minimumLinkPasswordRunes)
	}
	return nil
}
