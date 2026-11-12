package cairn

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type publisherAuthorKey struct{}

// credential is who an API request speaks for. An agent key speaks for one
// byline and reaches only that byline's pages; the installation token
// reaches every page and must name the byline itself.
type credential struct {
	// Agent is the key's byline, empty for the installation token.
	Agent string
	// Scope keeps idempotency keys apart per credential. It never holds
	// the secret: agent keys use the stored digest, which rotation changes.
	Scope string
}

func (c credential) installation() bool { return c.Agent == "" }

// reaches reports whether this credential may read or change a page with
// the given byline.
func (c credential) reaches(agent string) bool { return c.installation() || c.Agent == agent }

// kind names the credential for logs, never the secret.
func (c credential) kind() string {
	if c.installation() {
		return "installation_token"
	}
	return "agent_key"
}

// Why authenticate refused a request.
const (
	authMissing   = "missing"
	authMalformed = "malformed"
	authInvalid   = "invalid"
)

// authenticate reads the Bearer credential. Only hashes of agent keys are
// stored; the installation token is compared in constant time. failure is
// one of the auth* reasons when the request is refused.
func (s *Server) authenticate(r *http.Request) (c credential, failure string) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return c, authMissing
	}
	scheme, secret, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(secret) == "" {
		return c, authMalformed
	}
	secret = strings.TrimSpace(secret)
	if subtle.ConstantTimeCompare([]byte(secret), []byte(s.cfg.PublisherToken)) == 1 {
		return credential{Scope: "installation"}, ""
	}
	if !strings.HasPrefix(secret, "cairn_") || len(secret) > 128 {
		return c, authInvalid
	}
	digest := sha256.Sum256([]byte(secret))
	var name string
	// UPDATE RETURNING makes checking revocation and recording use one operation.
	if err := s.db.QueryRow(`UPDATE agent_keys SET last_used=? WHERE digest=? RETURNING name`, time.Now().UTC().Format(time.RFC3339), digest[:]).Scan(&name); err != nil {
		return c, authInvalid
	}
	return credential{Agent: name, Scope: "agent:" + hex.EncodeToString(digest[:])}, ""
}

// ownNameRefused is what the key dialog says when asked for a key on the
// owner's default author name.
const ownNameRefused = "This is your own name as the default author, so a key for it would reach every page you write or import. Give each agent its own name, such as Claude or Codex."

// isDefaultAuthor reports whether name is the owner's default author, the
// byline of everything the owner writes or imports in Cairn.
func (s *Server) isDefaultAuthor(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(s.preferences().Author))
}

// changeAgentKey answers the key dialog's fetch with JSON, or with one plain
// sentence that the dialog shows in place. Browsers never navigate here.
func (s *Server) changeAgentKey(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 100 || !utf8.ValidString(name) {
		http.Error(w, "Enter a name of up to 100 characters.", 400)
		return
	}
	action := r.FormValue("action")
	// A key scoped to the owner's own byline would reach every page the
	// owner wrote or imported. Revoking one made before this rule still works.
	if action != "key_revoke" && s.isDefaultAuthor(name) {
		http.Error(w, ownNameRefused, 400)
		return
	}
	var token string
	var digest []byte
	if action != "key_revoke" {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			http.Error(w, "Cairn couldn’t create a key. Try again.", 500)
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
	storage := func() {
		http.Error(w, "Cairn couldn’t change the key because its storage didn’t answer. Try again.", 500)
	}
	tx, err := s.db.Begin()
	if err != nil {
		storage()
		return
	}
	defer tx.Rollback()
	var previous []byte
	if err := tx.QueryRow(`SELECT digest FROM agent_keys WHERE name=?`, name).Scan(&previous); err != nil && err != sql.ErrNoRows {
		storage()
		return
	}
	result, err := tx.Exec(query, args...)
	if err != nil {
		storage()
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		http.Error(w, "This key changed in another window. Reload Settings and try again.", 409)
		return
	}
	// Replies stored for the old key can never be replayed now.
	if action != "key_create" && len(previous) > 0 {
		if err := forgetScope(tx, "agent:"+hex.EncodeToString(previous)); err != nil {
			storage()
			return
		}
	}
	if err := tx.Commit(); err != nil {
		storage()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"key": token, "name": name})
}
