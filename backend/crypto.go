package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

func randRead(b []byte) (int, error) { return rand.Read(b) }
func bytesToHex(b []byte) string     { return hex.EncodeToString(b) }

func deriveKey(secret []byte) []byte {
	h := sha256.Sum256(secret)
	return h[:]
}

func encryptAEAD(secret []byte, plaintext, context string) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("encryption key unavailable")
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(context))
	payload := append(nonce, ciphertext...)
	return "v2:" + base64.RawStdEncoding.EncodeToString(payload), nil
}

func decryptAEAD(secret []byte, encoded, context string) (string, error) {
	if !strings.HasPrefix(encoded, "v2:") {
		return "", errors.New("unsupported ciphertext version")
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, "v2:"))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte(context))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Legacy v5 helpers retained only for one-time migration.
func encryptAES(secret []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptAES(secret []byte, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *server) encryptDomainToken(hostname, token string) (string, error) {
	return encryptAEAD(s.masterKey, token, "cloudflare-token:"+hostname)
}

func (s *server) decryptDomainToken(hostname, encoded string) (string, error) {
	if strings.HasPrefix(encoded, "v2:") {
		return decryptAEAD(s.masterKey, encoded, "cloudflare-token:"+hostname)
	}
	if len(s.legacySecret) == 0 {
		return "", errors.New("legacy decryption key unavailable")
	}
	plain, err := decryptAES(s.legacySecret, encoded)
	if err != nil {
		return "", err
	}
	upgraded, err := s.encryptDomainToken(hostname, plain)
	if err == nil {
		_, _ = s.db.Exec(`UPDATE domains SET cloudflare_token_enc=? WHERE hostname=? AND cloudflare_token_enc=?`, upgraded, hostname, encoded)
	}
	return plain, nil
}

func (s *server) migrateEncryptedSecrets() error {
	rows, err := s.db.Query(`SELECT hostname, cloudflare_token_enc FROM domains WHERE cloudflare_token_enc IS NOT NULL AND cloudflare_token_enc != ''`)
	if err != nil {
		return err
	}
	type item struct{ host, enc string }
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.host, &it.enc); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	// Upgrade every legacy ciphertext and remove the legacy key in one
	// transaction. The previous best-effort update could leave an old ciphertext
	// behind and still delete session_secret, permanently orphaning the token.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, it := range items {
		if strings.HasPrefix(it.enc, "v2:") {
			if _, err := decryptAEAD(s.masterKey, it.enc, "cloudflare-token:"+it.host); err != nil {
				return fmt.Errorf("cannot verify encrypted token for %s: %w", it.host, err)
			}
			continue
		}
		if len(s.legacySecret) == 0 {
			return fmt.Errorf("cannot migrate encrypted token for %s: legacy decryption key unavailable", it.host)
		}
		plain, err := decryptAES(s.legacySecret, it.enc)
		if err != nil {
			return fmt.Errorf("cannot migrate encrypted token for %s: %w", it.host, err)
		}
		upgraded, err := s.encryptDomainToken(it.host, plain)
		if err != nil {
			return fmt.Errorf("cannot re-encrypt token for %s: %w", it.host, err)
		}
		res, err := tx.Exec(`UPDATE domains SET cloudflare_token_enc=? WHERE hostname=? AND cloudflare_token_enc=?`, upgraded, it.host, it.enc)
		if err != nil {
			return fmt.Errorf("cannot save migrated token for %s: %w", it.host, err)
		}
		if changed, err := res.RowsAffected(); err != nil || changed != 1 {
			if err != nil {
				return fmt.Errorf("cannot confirm migrated token for %s: %w", it.host, err)
			}
			return fmt.Errorf("cannot confirm migrated token for %s: row changed concurrently", it.host)
		}
	}
	if _, err := tx.Exec(`DELETE FROM config WHERE key='session_secret'`); err != nil {
		return fmt.Errorf("remove legacy decryption key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit encrypted token migration: %w", err)
	}
	s.legacySecret = nil
	return nil
}
