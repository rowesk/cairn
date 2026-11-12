package cairn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// passwordGate lets a GET or HEAD through to a shared page, or answers it
// with the password form (pages) or the locked notice (content and assets).
func (s *Server) passwordGate(w http.ResponseWriter, r *http.Request, p savedPage, hash, route, kind string, revision int64) bool {
	if hash == "" || s.validGrant(r, p.ID, revision) {
		return true
	}
	if kind == "page" {
		s.showPassword(w, p, "", http.StatusUnauthorized)
	} else {
		s.showVisitorError(w, http.StatusUnauthorized, problemLocked(route))
	}
	return false
}

// unlockShare answers a password posted to a shared page: a grant cookie and
// a redirect, or the gate again with what went wrong. It runs without the
// library lock, because bcrypt is slow on purpose.
func (s *Server) unlockShare(w http.ResponseWriter, r *http.Request, p savedPage, hash, route string, revision int64) {
	id := p.ID
	if hash == "" {
		// Nothing to unlock any more: show the page itself.
		http.Redirect(w, r, "/"+route, http.StatusSeeOther)
		return
	}
	if !sameOrigin(r) {
		s.showVisitorError(w, http.StatusForbidden, problemForeignUnlock(route))
		return
	}
	origin, _ := url.Parse(r.Header.Get("Origin"))
	ip := net.ParseIP(origin.Hostname())
	if origin.Scheme != "https" && (ip == nil || !ip.IsLoopback()) {
		s.showVisitorError(w, http.StatusForbidden, problemInsecure)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.showPassword(w, p, "Cairn couldn’t read that password. Enter it again.", http.StatusBadRequest)
		return
	}
	password := r.FormValue("password")
	if password == "" {
		s.showPassword(w, p, "Enter the password to read this page.", http.StatusBadRequest)
		return
	}
	if len(password) > 72 {
		s.showPassword(w, p, gateMismatch, http.StatusBadRequest)
		return
	}
	bucket, known := clientBucket(r)
	if until := s.unlocks.wait(id, bucket); until > 0 {
		seconds := int(math.Ceil(until.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		s.showPassword(w, p, gateWait(seconds), http.StatusTooManyRequests, seconds)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		s.unlocks.fail(id, bucket, known)
		// Say now when the next try can be made, so it never meets a refusal.
		message, seconds := gateMismatch, 0
		if until := s.unlocks.wait(id, bucket); until > 0 {
			seconds = int(math.Ceil(until.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			message = gateMismatchWait(seconds)
		}
		s.showPassword(w, p, message, http.StatusUnauthorized, seconds)
		return
	}
	s.unlocks.succeed(id, bucket)
	// SameSite=None permits images requested from the opaque report iframe.
	// Public deployment must use HTTPS; loopback Chromium supports secure cookies for local checks.
	http.SetCookie(w, &http.Cookie{Name: "cairn_share_" + id, Value: s.grant(id, revision), Path: "/", MaxAge: 43200, Secure: true, HttpOnly: true, SameSite: http.SameSiteNoneMode})
	http.Redirect(w, r, "/"+route, http.StatusSeeOther)
}

// Unlock limits. One wrong password costs nothing, so a visitor who mistypes
// and retypes at once gets in. From the second wrong password in a row the
// same page waits before it takes another try from the same address: 1
// second, then 2, 4 and so on up to 5 minutes. Wrong passwords on a page also
// slow that page for everyone, but only up to 10 seconds, so someone guessing
// elsewhere can never keep a real visitor waiting long. Both forget a page
// after 15 quiet minutes, and the limiter keeps at most a few thousand
// entries.
const (
	unlockClientCap  = 5 * time.Minute
	unlockSharedCap  = 10 * time.Second
	unlockQuiet      = 15 * time.Minute
	unlockMaxEntries = 4096
)

type unlockFailures struct {
	count       int
	last, until time.Time
}

type unlockLimiter struct {
	mu      sync.Mutex
	clients map[string]*unlockFailures // page id, NUL, address bucket
	pages   map[string]*unlockFailures // page id
	now     func() time.Time
}

func newUnlockLimiter() *unlockLimiter {
	return &unlockLimiter{clients: map[string]*unlockFailures{}, pages: map[string]*unlockFailures{}, now: time.Now}
}

// backoff is the wait after the n-th failure in a row: none after the first,
// then 1s doubling to cap.
func backoff(n int, cap time.Duration) time.Duration {
	if n <= 1 {
		return 0
	}
	return min(time.Second<<min(n-2, 20), cap)
}

// entry finds a live entry, forgetting one that has been quiet long enough.
func (l *unlockLimiter) entry(m map[string]*unlockFailures, key string, now time.Time) *unlockFailures {
	f := m[key]
	if f != nil && now.Sub(f.last) > unlockQuiet {
		delete(m, key)
		return nil
	}
	return f
}

// wait is how long a try on page from bucket must wait.
func (l *unlockLimiter) wait(page, bucket string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	if f := l.entry(l.clients, page+"\x00"+bucket, now); f != nil {
		wait = f.until.Sub(now)
	}
	if f := l.entry(l.pages, page, now); f != nil {
		wait = max(wait, f.until.Sub(now))
	}
	return max(wait, 0)
}

// fail records a wrong password. known is false when Cairn cannot tell
// visitors apart (a proxy that names no client), so that bucket gets only
// the short shared cap.
func (l *unlockLimiter) fail(page, bucket string, known bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	clientCap := unlockClientCap
	if !known {
		clientCap = unlockSharedCap
	}
	l.bump(l.clients, page+"\x00"+bucket, clientCap, now)
	l.bump(l.pages, page, unlockSharedCap, now)
}

func (l *unlockLimiter) bump(m map[string]*unlockFailures, key string, cap time.Duration, now time.Time) {
	f := l.entry(m, key, now)
	if f == nil {
		if len(m) >= unlockMaxEntries {
			l.sweep(m, now)
		}
		f = &unlockFailures{}
		m[key] = f
	}
	f.count++
	f.last = now
	f.until = now.Add(backoff(f.count, cap))
}

// sweep drops quiet entries, then the oldest one if the map is still full.
func (l *unlockLimiter) sweep(m map[string]*unlockFailures, now time.Time) {
	var oldest string
	for key, f := range m {
		if now.Sub(f.last) > unlockQuiet {
			delete(m, key)
		} else if oldest == "" || f.last.Before(m[oldest].last) {
			oldest = key
		}
	}
	if len(m) >= unlockMaxEntries && oldest != "" {
		delete(m, oldest)
	}
}

// succeed clears the address's own failures. The page's shared count stays,
// so one right password does not reset a guesser elsewhere.
func (l *unlockLimiter) succeed(page, bucket string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, page+"\x00"+bucket)
}

// forget drops everything about a deleted page.
func (l *unlockLimiter) forget(page string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pages, page)
	for key := range l.clients {
		if strings.HasPrefix(key, page+"\x00") {
			delete(l.clients, key)
		}
	}
}

// clientBucket groups requests by where they come from: one IPv4 address or
// one IPv6 /64. Behind a proxy on this machine, the last X-Forwarded-For
// address is the one the proxy saw. known is false when nothing names the
// client, so every visitor shares one bucket.
func clientBucket(r *http.Request) (bucket string, known bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown", false
	}
	addr = addr.Unmap().WithZone("")
	if addr.IsLoopback() {
		forwarded, ok := lastForwarded(r)
		if !ok {
			return "local", false
		}
		addr = forwarded
	}
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String(), true
	}
	return addr.String(), true
}

// lastForwarded is the rightmost address in X-Forwarded-For: the one the
// nearest proxy added, which a visitor cannot choose.
func lastForwarded(r *http.Request) (netip.Addr, bool) {
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return netip.Addr{}, false
	}
	parts := strings.Split(values[len(values)-1], ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if ap, err := netip.ParseAddrPort(last); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	addr, err := netip.ParseAddr(strings.Trim(last, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

// passwordStamp ties a "checked length" mark to the hash it vouches for, so
// a password changed by an older Cairn, which never records one, shows as
// unchecked again.
func passwordStamp(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:8])
}

const gateMismatch = "That password didn’t match. Check it and try again."

// waitWords says how long a wait is: "1 second", "8 seconds", "2 minutes".
func waitWords(seconds int) string {
	switch {
	case seconds <= 1:
		return "1 second"
	case seconds < 90:
		return strconv.Itoa(seconds) + " seconds"
	}
	return strconv.Itoa((seconds+59)/60) + " minutes"
}

// gateMismatchWait answers a wrong password that starts a pause.
func gateMismatchWait(seconds int) string {
	return "That password didn’t match. You can try again in " + waitWords(seconds) + "."
}

// gateWait answers a try made before a pause ended. Cairn pauses after
// repeated wrong passwords, longer each time, so a guesser cannot try many
// in a row.
func gateWait(seconds int) string {
	return "Wait " + waitWords(seconds) + ", then try again. Cairn pauses after repeated wrong passwords."
}

// gateData is everything the password gate shows. It names the agent that
// prepared the page, as the public reader does, but never the page title,
// which can say more than its owner meant to share before the password.
// Wait is how many seconds the next try must wait; the page's script holds
// a submit until then, so what was typed stays in the field.
type gateData struct {
	Theme, Agent, Message string
	Photo                 template.URL
	Wait                  int
}

func (s *Server) showPassword(w http.ResponseWriter, p savedPage, message string, status int, wait ...int) {
	formPageHeaders(w)
	w.WriteHeader(status)
	d := gateData{Theme: s.themes().public(), Agent: p.Agent, Photo: s.authorPhoto(p.Agent), Message: message}
	if len(wait) > 0 {
		d.Wait = wait[0]
	}
	_ = passwordView.Execute(w, d)
}
