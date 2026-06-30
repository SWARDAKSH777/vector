package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func stableValueHash(key []byte, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strings.TrimSpace(value)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func idString(id int64) string { return strconv.FormatInt(id, 10) }

func (s *server) audit(r *http.Request, actorID int64, event, targetType, targetID string, metadata map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > 8192 {
		encoded = []byte(`{"metadata":"omitted"}`)
	}
	var actor any
	if actorID > 0 {
		actor = actorID
	}
	ipHash := ""
	if r != nil {
		ipHash = stableValueHash(s.masterKey, requestClientIP(r))
	}
	_, _ = s.db.Exec(`INSERT INTO audit_logs(actor_id,event,target_type,target_id,ip_hash,metadata)
		VALUES(?,?,?,?,?,?)`, actor, event, targetType, targetID, ipHash, string(encoded))
}
