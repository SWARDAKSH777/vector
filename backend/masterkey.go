package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const masterKeySize = 32

func loadMasterKey(db *sql.DB, dataDir string) ([]byte, []byte, error) {
	path := getenv("VECTOR_MASTER_KEY_FILE", "/etc/vector/master.key")
	encoded, err := os.ReadFile(path)
	if err != nil {
		// Developer/test fallback only. Production installer always creates the
		// root-owned /etc/vector/master.key before starting the service.
		if strings.HasPrefix(filepath.Clean(dataDir), "/opt/vector") {
			return nil, nil, fmt.Errorf("master key unavailable at %s: %w", path, err)
		}
		path = filepath.Join(dataDir, ".master.key")
		encoded, err = os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			key := make([]byte, masterKeySize)
			if _, err := rand.Read(key); err != nil {
				return nil, nil, err
			}
			if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
				return nil, nil, err
			}
			encoded = []byte(base64.RawStdEncoding.EncodeToString(key))
		} else if err != nil {
			return nil, nil, err
		}
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(key) != masterKeySize {
		return nil, nil, fmt.Errorf("master key must be a base64-encoded %d-byte value", masterKeySize)
	}

	// v5 encrypted tokens were derived from the database session_secret. Keep it
	// only long enough to migrate those values to the external key.
	var legacy string
	if err := db.QueryRow(`SELECT value FROM config WHERE key='session_secret'`).Scan(&legacy); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("load legacy encryption secret: %w", err)
	}
	return key, []byte(legacy), nil
}
