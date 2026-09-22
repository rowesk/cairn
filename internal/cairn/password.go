package cairn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) loadShareKey() error {
	path := filepath.Join(s.cfg.DataDir, "share.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = []byte(randomID() + randomID())
		if err = writeSynced(path, key); err != nil {
			return err
		}
		if err = syncDir(s.cfg.DataDir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if len(key) != 64 {
		return fmt.Errorf("invalid share signing key")
	}
	s.shareKey = key
	return nil
}
func (s *Server) grant(id string, revision int64) string {
	payload := fmt.Sprintf("%s:%d:%d", id, revision, time.Now().Add(12*time.Hour).Unix())
	mac := hmac.New(sha256.New, s.shareKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) validGrant(r *http.Request, id string, revision int64) bool {
	cookie, err := r.Cookie("cairn_share_" + id)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.shareKey)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	fields := strings.Split(string(payload), ":")
	if len(fields) != 3 || fields[0] != id || fields[1] != strconv.FormatInt(revision, 10) {
		return false
	}
	expires, err := strconv.ParseInt(fields[2], 10, 64)
	return err == nil && expires > time.Now().Unix()
}
func (s *Server) passwordGate(w http.ResponseWriter, r *http.Request, id, hash, route, kind string, revision int64) bool {
	if hash == "" {
		if r.Method == http.MethodPost {
			http.Error(w, "this page does not need a password", 400)
			return false
		}
		return true
	}
	if r.Method != http.MethodPost {
		if s.validGrant(r, id, revision) {
			return true
		}
		if kind == "page" {
			showPassword(w, "", 401)
		} else {
			http.Error(w, "password required", 401)
		}
		return false
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https") {
		http.Error(w, "open the report and use its password form", 403)
		return false
	}
	ip := net.ParseIP(origin.Hostname())
	if origin.Scheme != "https" && (ip == nil || !ip.IsLoopback()) {
		http.Error(w, "password sharing requires HTTPS", 403)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		showPassword(w, "Invalid password request.", 400)
		return false
	}
	password := r.FormValue("password")
	if len(password) == 0 || len(password) > 72 {
		showPassword(w, "Enter a password of at most 72 bytes.", 400)
		return false
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if time.Now().Before(s.failedUnlock[id]) {
		w.Header().Set("Retry-After", "1")
		showPassword(w, "Wait a moment before trying again.", 429)
		return false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		s.failedUnlock[id] = time.Now().Add(time.Second)
		showPassword(w, "That password did not match.", 401)
		return false
	}
	delete(s.failedUnlock, id)
	// SameSite=None permits images requested from the opaque report iframe.
	// Public deployment must use HTTPS; loopback Chromium supports secure cookies for local checks.
	http.SetCookie(w, &http.Cookie{Name: "cairn_share_" + id, Value: s.grant(id, revision), Path: "/", MaxAge: 43200, Secure: true, HttpOnly: true, SameSite: http.SameSiteNoneMode})
	http.Redirect(w, r, "/"+route, http.StatusSeeOther)
	return false
}
func showPassword(w http.ResponseWriter, message string, status int) {
	privateFormHeaders(w)
	w.WriteHeader(status)
	passwordView.Execute(w, message)
}

var passwordView = template.Must(template.New("password").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Unlock report</title><style>` + ownerCSS + `</style><h1>This report needs a password</h1>{{if .}}<p role="alert">{{.}}</p>{{end}}<form method="post" class="actions"><label>Password <input type="password" name="password" required autocomplete="current-password"></label><button>Unlock report</button></form>` + focusScriptTag + `</html>`))
