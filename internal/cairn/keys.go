package cairn

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type publisherAuthorKey struct{}

// Only hashes are persisted. The existing installation credential remains valid.
func (s *Server) publisherIdentity(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+s.cfg.PublisherToken)) == 1 {
		return "", true
	}
	if !strings.HasPrefix(header, "Bearer cairn_") || len(header) > 128 {
		return "", false
	}
	digest := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
	var name string
	// UPDATE RETURNING makes checking revocation and recording use one operation.
	err := s.db.QueryRow(`UPDATE agent_keys SET last_used=? WHERE digest=? RETURNING name`, time.Now().UTC().Format(time.RFC3339), digest[:]).Scan(&name)
	return name, err == nil
}

func (s *Server) changeAgentKey(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 100 || !utf8.ValidString(name) {
		http.Error(w, "Enter an author name up to 100 bytes.", 400)
		return
	}
	action := r.FormValue("action")
	var token string
	var digest []byte
	if action != "key_revoke" {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			http.Error(w, "Could not generate a key. Try again.", 500)
			return
		}
		token = "cairn_" + base64.RawURLEncoding.EncodeToString(raw[:])
		sum := sha256.Sum256([]byte(token))
		digest = sum[:]
	}
	var query string
	switch action {
	case "key_create":
		query = `INSERT INTO agent_keys(name,digest,created_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET digest=excluded.digest,created_at=excluded.created_at,last_used='' WHERE agent_keys.digest IS NULL`
	case "key_rotate":
		query = `UPDATE agent_keys SET digest=?,created_at=?,last_used='' WHERE name=? AND digest IS NOT NULL`
	case "key_revoke":
		query = `UPDATE agent_keys SET digest=NULL WHERE name=? AND digest IS NOT NULL`
	}
	args := []any{name, digest, time.Now().UTC().Format(time.RFC3339)}
	if action == "key_rotate" {
		args = []any{digest, time.Now().UTC().Format(time.RFC3339), name}
	}
	if action == "key_revoke" {
		args = []any{name}
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		http.Error(w, "Could not update the key. Try again.", 500)
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		http.Error(w, "Key changed elsewhere. Reload settings and try again.", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"key": token, "name": name})
}
