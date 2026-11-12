package cairn

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Regression tests for the Phase 7A security review, one or more per
// finding. Each failed against the code before its fix.

type secLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *secLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *secLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type secApp struct {
	*fxApp
	logs *secLog
}

// secStart runs a library with sharing on and the given changes to its
// configuration.
func secStart(t *testing.T, dir string, change func(*Config)) *secApp {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	logs := &secLog{}
	cfg := Config{DataDir: dir, PublisherToken: fxToken, PublicBaseURL: "https://reports.example.com", Logger: slog.New(slog.NewTextHandler(logs, nil))}
	if change != nil {
		change(&cfg)
	}
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.waitIndexed()
	a := &secApp{fxApp: &fxApp{s: s, private: httptest.NewServer(s), public: httptest.NewServer(s.PublicHandler()), dir: dir}, logs: logs}
	t.Cleanup(a.close)
	return a
}

// send is fxApp.do with a Host header of the caller's choosing.
func (a *secApp) send(t *testing.T, method, path, host string, body io.Reader, header map[string]string) (int, http.Header, string) {
	t.Helper()
	base := a.private.URL
	if strings.HasPrefix(path, "public:") {
		base, path = a.public.URL, strings.TrimPrefix(path, "public:")
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	client := *a.private.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(data)
}

func hostOf(raw string) string {
	u, _ := url.Parse(raw)
	return u.Host
}

// Finding 1: DNS rebinding. Each listener answers only the names its owner
// reaches it by, before any handler runs.
func TestHostAllowList(t *testing.T) {
	a := secStart(t, "", func(c *Config) {
		c.PrivateBaseURL = "https://cairn.home.example"
		c.AllowedHosts = []string{"mac-mini.lan", "other.example:8443"}
	})
	a.publish(t, "POST", "/api/pages", fxPage("plan", "Plan", "PRIVATE-REQUEST", "Body."))
	port := strings.TrimPrefix(a.private.URL, "http://127.0.0.1:")
	for _, host := range []string{"attacker.example", "attacker.example:" + port, "127.0.0.1.attacker.example", "cairn.home.example.attacker.example", "box.ts.net.attacker.example", "localhost.attacker.example"} {
		for _, path := range []string{"/", "/settings", "/healthz", "/plan", "/api/pages"} {
			status, header, body := a.send(t, "GET", path, host, nil, map[string]string{"Authorization": "Bearer " + fxToken})
			if status != http.StatusMisdirectedRequest || !strings.Contains(body, "CAIRN_ALLOWED_HOSTS") || strings.Contains(body, `name="csrf"`) || strings.Contains(body, "data-csrf") || strings.Contains(body, "PRIVATE-REQUEST") || !strings.Contains(header.Get("Content-Security-Policy"), "default-src 'none'") {
				t.Errorf("private %s%s: %d %.200s", host, path, status, body)
			}
		}
	}
	if !strings.Contains(a.logs.String(), `msg="host refused"`) || !strings.Contains(a.logs.String(), "host=attacker.example") {
		t.Errorf("refusals were not logged: %s", a.logs.String())
	}
	// A rebinding page posting with a matching Origin changes nothing.
	values := url.Values{"csrf": {a.s.formToken().String()}, "action": {"share"}}
	status, _, _ := a.send(t, "POST", "/manage/plan", "attacker.example:"+port, strings.NewReader(values.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "http://attacker.example:" + port})
	var shared bool
	a.s.db.QueryRow(`SELECT share_enabled FROM pages WHERE slug='plan'`).Scan(&shared)
	if status != http.StatusMisdirectedRequest || shared {
		t.Errorf("rebound post: %d, shared %v", status, shared)
	}
	values = url.Values{"csrf": {a.s.formToken().String()}, "action": {"key_create"}, "name": {"Mallory"}}
	status, _, _ = a.send(t, "POST", "/settings", "attacker.example:"+port, strings.NewReader(values.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "http://attacker.example:" + port})
	var keys int
	a.s.db.QueryRow(`SELECT count(*) FROM agent_keys`).Scan(&keys)
	if status != http.StatusMisdirectedRequest || keys != 0 {
		t.Errorf("rebound key: %d, %d keys", status, keys)
	}
	for _, host := range []string{"127.0.0.1:" + port, "[::1]:" + port, "localhost:" + port, "LOCALHOST", "raspberrypi", "raspberrypi:8080", "box.tail1234.ts.net", "box.tail1234.ts.net.", "100.101.102.103:8080", "[fd7a:115c:a1e0::1]:8080", "cairn.home.example", "CAIRN.HOME.EXAMPLE", "mac-mini.lan", "other.example"} {
		if status, _, body := a.send(t, "GET", "/", host, nil, nil); status != 200 || !strings.Contains(body, `data-csrf=`) {
			t.Errorf("private %s refused: %d", host, status)
		}
	}
	// The owner's own forms still work through an allowed name.
	values = url.Values{"csrf": {a.s.formToken().String()}, "action": {"rename"}, "title": {"Plan B"}}
	if status, _, body := a.send(t, "POST", "/manage/plan", "raspberrypi", strings.NewReader(values.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "http://raspberrypi"}); status != 303 {
		t.Errorf("owner form through a tailnet name: %d %.200s", status, body)
	}
	// The public listener answers its own public name and loopback only.
	for _, host := range []string{"attacker.example", "raspberrypi", "box.tail1234.ts.net", "cairn.home.example", "100.101.102.103", "mac-mini.lan"} {
		status, _, body := a.send(t, "GET", "public:/healthz", host, nil, nil)
		if status != http.StatusMisdirectedRequest || strings.Contains(body, "CAIRN_") || strings.Contains(body, "library") {
			t.Errorf("public %s: %d %.200s", host, status, body)
		}
	}
	for _, host := range []string{"reports.example.com", "REPORTS.example.com:443", hostOf(a.public.URL), "localhost:8081", "[::1]:8081"} {
		if status, _, _ := a.send(t, "GET", "public:/healthz", host, nil, nil); status != 200 {
			t.Errorf("public %s refused: %d", host, status)
		}
	}
	if !strings.Contains(a.logs.String(), "listener=public") {
		t.Error("public refusals were not logged")
	}
}

func TestHostNamesAndOrigins(t *testing.T) {
	private := newHostPolicy(true, []string{"https://cairn.home.example"}, []string{"mac-mini.lan"})
	for host, want := range map[string]bool{
		"127.0.0.1": true, "127.0.0.1:80": true, "[::1]": true, "[::1]:8080": true, "::1": false, "[::1": false,
		"10.0.0.2": true, "localhost": true, "pi": true, "pi.": true, "x.ts.net": true, "cairn.home.example": true,
		"mac-mini.lan:8080": true, "mac-mini.lan.evil.example": false, "evil.example": false, "evil.example:": false,
		"evil.example:80x": false, "": false, "-bad": false, "a..b": false, "ts.net.evil.example": false,
	} {
		if got := private.allows(host); got != want {
			t.Errorf("private allows(%q) = %v, want %v", host, got, want)
		}
	}
	public := newHostPolicy(false, []string{"https://reports.example.com"}, nil)
	for host, want := range map[string]bool{"reports.example.com": true, "127.0.0.1:8081": true, "[::1]": true, "localhost": true, "10.0.0.2": false, "pi": false, "x.ts.net": false} {
		if got := public.allows(host); got != want {
			t.Errorf("public allows(%q) = %v, want %v", host, got, want)
		}
	}
	// Origin checks use the listener's list, not just equality with Host.
	r := httptest.NewRequest("POST", "/settings", nil)
	r.Host = "evil.example"
	r.Header.Set("Origin", "http://evil.example")
	if !sameOrigin(r) || sameOrigin(withHostPolicy(r, private)) {
		t.Error("the Origin check ignores the allow-list")
	}
	if err := checkAllowedHosts([]string{"ok.example", "bad name"}); err == nil {
		t.Error("a bad allowed host was accepted")
	}
}

// Finding 3: page and deleted can't aim a form or put words in a notice.
func TestNoticeParametersCarryNoChosenText(t *testing.T) {
	a := secStart(t, "", func(c *Config) {
		c.Mail = MailConfig{Server: "127.0.0.1:9", From: "me@example.com", Password: "x"}
	})
	a.publish(t, "POST", "/api/pages", fxPage("real", "Real page", "Request", "Body."))
	for _, page := range []string{"../settings", "real/../../settings", "nothing-here", "settings", "%2e%2e"} {
		_, body := a.get(t, "/?kindle=failed&page="+url.QueryEscape(page))
		if strings.Contains(body, `action="/manage/`) || !strings.Contains(body, "couldn’t send this page") {
			t.Errorf("page=%q built a form: %s", page, regexp.MustCompile(`<form[^>]*action="/manage/[^"]*"`).FindString(body))
		}
	}
	if _, body := a.get(t, "/?kindle=failed&page=real"); !strings.Contains(body, `action="/manage/real"`) {
		t.Error("a real page lost its Try again form")
	}
	if _, body := a.get(t, "/?done=archived&page=..%2Fsettings"); strings.Contains(body, ">Undo</button>") {
		t.Error("archive notice for a page that does not exist")
	}
	_, body := a.get(t, "/?deleted=Your+session+expired.+Call+555-0100")
	if strings.Contains(body, "session expired") || !strings.Contains(body, "Page deleted.") {
		t.Errorf("deleted carried text: %s", regexp.MustCompile(`Deleted[^<]*|Page deleted[^<]*`).FindString(body))
	}
	status, header, _ := a.form(t, "/manage/real/delete", url.Values{"action": {"delete"}})
	location := header.Get("Location")
	if status != 303 || !regexp.MustCompile(`^/\?deleted=[0-9a-f]{16}$`).MatchString(location) {
		t.Fatalf("delete: %d %s", status, location)
	}
	if _, body := a.get(t, location); !strings.Contains(body, "Deleted “Real page”.") {
		t.Error("the library did not name the deleted page")
	}
	cookie := header.Get("Set-Cookie")
	if !strings.HasPrefix(cookie, "cairn_deleted=") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Errorf("the browser was not told to drop the draft: %q", cookie)
	}
}

// Finding 4: a return address that url.Parse rejects never panics.
func TestArchiveReturnIsAlwaysALocalPath(t *testing.T) {
	a := secStart(t, "", nil)
	a.publish(t, "POST", "/api/pages", fxPage("a", "Archive me", "Request", "Body."))
	for back, want := range map[string]string{
		"/\tx": "/?done=archived&page=a", "/\x00": "/?done=archived&page=a", "/a\x7fb": "/?done=archived&page=a",
		"/%zz": "/?done=archived&page=a", "//evil.example/": "/?done=archived&page=a", "/\\evil.example": "/?done=archived&page=a",
		"https://evil.example/": "/?done=archived&page=a", "javascript:alert(1)": "/?done=archived&page=a", "": "/?done=archived&page=a",
		"/?q=lamps": "/?done=archived&page=a&q=lamps", "/a?in=q%3Dx": "/a?done=archived&in=q%3Dx&page=a",
	} {
		status, header, body := a.form(t, "/_library/archive", url.Values{"slug": {"a"}, "archived": {"1"}, "return": {back}})
		if status != 303 || header.Get("Location") != want {
			t.Errorf("return %q: %d %q %.120s", back, status, header.Get("Location"), body)
		}
	}
	if status, _ := a.get(t, "/"); status != 200 {
		t.Error("the server stopped answering")
	}
	for raw, want := range map[string]string{"/ok": "/ok", "/\r\n": "/", "//x": "/", "/\\x": "/", "x": "/", "/" + strings.Repeat("a", 3000): "/"} {
		if got := localPath(raw, "/"); got != want {
			t.Errorf("localPath(%q) = %q", raw, got)
		}
	}
}

// Finding 6: the token is masked afresh wherever a page prints it.
func TestFormTokenIsMaskedPerPage(t *testing.T) {
	a := secStart(t, "", nil)
	a.publish(t, "POST", "/api/pages", fxPage("plan", "Plan", "Request", "Body."))
	plain := hex.EncodeToString(a.s.csrf)
	token := regexp.MustCompile(`(?:name="csrf" value|data-csrf)="([^"]+)"`)
	seen := map[string]bool{}
	for _, path := range []string{"/", "/?q=pl", "/settings", "/manage/plan", "/new", "/edit/plan", "/import", "/plan", "/?fragment=list"} {
		for range 2 {
			_, body := a.get(t, path)
			if strings.Contains(body, plain) {
				t.Errorf("%s prints the plain token", path)
			}
			matches := token.FindAllStringSubmatch(body, -1)
			if len(matches) == 0 && path != "/?fragment=list" {
				t.Errorf("%s has no token", path)
			}
			for _, m := range matches {
				if len(m[1]) != 128 || seen[m[1]] || !a.s.tokenMatches(m[1]) {
					t.Errorf("%s: token %q repeats or does not check", path, m[1])
				}
				seen[m[1]] = true
			}
		}
	}
	masked := a.s.formToken().String()
	flip := "0"
	if masked[127] == '0' {
		flip = "1"
	}
	tampered := masked[:127] + flip
	if a.s.tokenMatches(tampered) || a.s.tokenMatches("") || a.s.tokenMatches(strings.Repeat("0", 128)) || !a.s.tokenMatches(plain) {
		t.Error("token check accepts a wrong token or refuses the plain one")
	}
	if status, _, _ := a.do(t, "GET", "/_library/request/plan", nil, map[string]string{"X-Cairn-Token": masked, "Sec-Fetch-Site": "same-origin"}); status != 200 {
		t.Errorf("masked header token refused: %d", status)
	}
	if status, _, _ := a.form(t, "/manage/plan", url.Values{"csrf": {masked}, "action": {"rename"}, "title": {"Masked"}}); status != 303 {
		t.Errorf("masked form token refused: %d", status)
	}
}

// Finding 7: stored replies hold no page content and do not outlive their
// use.
func TestIdempotencyKeepsNoContent(t *testing.T) {
	dir := t.TempDir()
	a := secStart(t, dir, nil)
	key := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + fxToken, "Idempotency-Key": "first-try"}
	body, _ := json.Marshal(fxPage("kept", "SECRET-TITLE", "SECRET-REQUEST", "SECRET-BODY"))
	status, _, first := a.do(t, "POST", "/api/pages", bytes.NewReader(body), key)
	if status != 201 {
		t.Fatal(first)
	}
	var stored []byte
	var pageID string
	if err := a.s.db.QueryRow(`SELECT response,page_id FROM idempotency_keys`).Scan(&stored, &pageID); err != nil || pageID == "" || bytes.Contains(stored, []byte("SECRET")) {
		t.Fatalf("stored reply: %v %q %s", err, pageID, stored)
	}
	// A replay rebuilds the answer from the page as it is now.
	a.do(t, "PATCH", "/api/pages/kept", strings.NewReader(`{"title":"Renamed"}`), map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + fxToken})
	status, header, replay := a.do(t, "POST", "/api/pages", bytes.NewReader(body), key)
	var page apiPageWrite
	json.Unmarshal([]byte(replay), &page)
	if status != 201 || header.Get("Idempotent-Replayed") != "true" || header.Get("Location") != "/kept" || page.Title != "Renamed" || page.OriginalRequest != "SECRET-REQUEST" || page.ID != pageID {
		t.Fatalf("replay: %d %v %s", status, header, replay)
	}
	// Replies from earlier versions are slimmed, expired ones go at start.
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	a.s.db.Exec(`INSERT INTO idempotency_keys(scope,key,fingerprint,status,response,created_at,page_id) VALUES('installation','expired','f',201,'{}',?,'x')`, old)
	a.s.db.Exec(`INSERT INTO idempotency_keys(scope,key,fingerprint,status,response,created_at) VALUES('installation','legacy','f',201,?,?)`, first, now)
	a.close()
	b := secStart(t, dir, nil)
	var count int
	b.s.db.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE key='expired'`).Scan(&count)
	b.s.db.QueryRow(`SELECT response,page_id FROM idempotency_keys WHERE key='legacy'`).Scan(&stored, &pageID)
	if count != 0 || pageID == "" || bytes.Contains(stored, []byte("SECRET")) {
		t.Errorf("startup kept %d expired replies; legacy reply %q %s", count, pageID, stored)
	}
	b.s.db.Exec(`INSERT INTO idempotency_keys(scope,key,fingerprint,status,response,created_at,page_id) VALUES('installation','late','f',201,'{}',?,'x')`, old)
	if err := b.s.purgeReplies(); err != nil {
		t.Fatal(err)
	}
	if b.s.db.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE key='late'`).Scan(&count); count != 0 {
		t.Error("the timed purge kept an expired reply")
	}
	// Rotating or revoking a key drops its replies; deleting a page drops
	// the replies that name it.
	agent := newAgentKey(t, b, "Researcher")
	agentKey := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + agent, "Idempotency-Key": "agent-try"}
	body, _ = json.Marshal(map[string]any{"title": "Agent page", "original_request": "r", "markdown": "x", "slug": "agent-page"})
	b.do(t, "POST", "/api/pages", bytes.NewReader(body), agentKey)
	countFor := func(scope string) int {
		var n int
		b.s.db.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE scope LIKE ?`, scope).Scan(&n)
		return n
	}
	if countFor("agent:%") != 1 {
		t.Fatal("the agent's reply was not stored")
	}
	if status, _, body := b.form(t, "/settings", url.Values{"action": {"key_rotate"}, "name": {"Researcher"}}); status != 200 {
		t.Fatal(body)
	}
	if countFor("agent:%") != 0 {
		t.Error("rotation kept the old key's replies")
	}
	b.form(t, "/manage/kept/delete", url.Values{"action": {"delete"}})
	if countFor("installation") != 0 {
		t.Error("deleting the page kept its replies")
	}
}

func newAgentKey(t *testing.T, a *secApp, name string) string {
	t.Helper()
	status, _, body := a.form(t, "/settings", url.Values{"action": {"key_create"}, "name": {name}})
	var reply struct{ Key string }
	if status != 200 || json.Unmarshal([]byte(body), &reply) != nil || reply.Key == "" {
		t.Fatalf("key for %s: %d %s", name, status, body)
	}
	return reply.Key
}

// Finding 8: a page replaced, renamed or deleted by an older Cairn, which
// knows nothing of the search index, is put right at the next start.
func TestIndexReconcilesAfterAnOlderBinary(t *testing.T) {
	dir := t.TempDir()
	a := secStart(t, dir, nil)
	a.publish(t, "POST", "/api/pages", fxPage("kept", "Zebra notes", "Request", "Words about zebras."))
	a.publish(t, "POST", "/api/pages", fxPage("gone", "Walrus notes", "Request", "Words about walruses."))
	var goneID, object string
	a.s.db.QueryRow(`SELECT id FROM pages WHERE slug='gone'`).Scan(&goneID)
	a.s.db.QueryRow(`SELECT object FROM pages WHERE slug='kept'`).Scan(&object)
	a.close()
	// What an older Cairn does: a new object and row values, no index.
	db, err := sql.Open("sqlite", filepath.Join(dir, "cairn.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := randomID()
	if err := os.MkdirAll(filepath.Join(dir, "objects", fresh), 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "objects", fresh, "page.html"), []byte(`<!doctype html><html><head></head><body><main data-template="report"><p>Words about quokkas now.</p></main></body></html>`), 0600)
	if _, err := db.Exec(`UPDATE pages SET object=?,title='Quokka notes' WHERE slug='kept'`, fresh); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(dir, "objects", object))
	if _, err := db.Exec(`DELETE FROM pages WHERE slug='gone'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	b := secStart(t, dir, nil)
	search := func(q string) []string {
		items, _, err := b.s.listPages(libraryQuery{Text: q, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var slugs []string
		for _, item := range items {
			slugs = append(slugs, item.Slug)
		}
		return slugs
	}
	if got := strings.Join(search("quokkas"), ","); got != "kept" {
		t.Errorf("new words: %q", got)
	}
	if got := strings.Join(search("zebras"), ","); got != "" {
		t.Errorf("old words still match: %q", got)
	}
	if got := strings.Join(search("Quokka notes"), ","); got != "kept" {
		t.Errorf("new title: %q", got)
	}
	var orphans int
	b.s.db.QueryRow(`SELECT count(*) FROM pages_fts WHERE page_id=?`, goneID).Scan(&orphans)
	if orphans != 0 {
		t.Error("the deleted page's index row survived")
	}
	// Pages this version indexed are left alone at the next start.
	var stamp string
	b.s.db.QueryRow(`SELECT index_source FROM pages WHERE slug='kept'`).Scan(&stamp)
	if stamp == "" {
		t.Error("the rebuilt row has no stamp")
	}
}

// Finding 9: a power cut between adding a column and filling it must not
// leave the column empty. A trigger stands in for the cut: it stops the
// backfill after the ALTER TABLE.
func TestColumnAddAndBackfillAreOneStep(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE pages (id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, title TEXT NOT NULL, agent TEXT NOT NULL, original_request TEXT NOT NULL, created_at TEXT NOT NULL, object TEXT NOT NULL UNIQUE, archived INTEGER NOT NULL DEFAULT 0, share_enabled INTEGER NOT NULL DEFAULT 0, public_slug TEXT UNIQUE, password_hash TEXT, request_summary TEXT NOT NULL DEFAULT '');
 INSERT INTO pages(id,slug,title,agent,original_request,created_at,object) VALUES('p','old','Old','A','r','2026-01-02T03:04:05Z','o');
 CREATE TABLE theme_preferences (id INTEGER PRIMARY KEY CHECK(id=1),admin TEXT NOT NULL,reader TEXT NOT NULL,public TEXT NOT NULL);
 INSERT INTO theme_preferences VALUES(1,'cairn','cairn','minimal');
 CREATE TRIGGER cut BEFORE UPDATE ON pages BEGIN SELECT RAISE(ABORT, 'power cut'); END;
 CREATE TRIGGER cut_themes BEFORE UPDATE ON theme_preferences BEGIN SELECT RAISE(ABORT, 'power cut'); END;`); err != nil {
		t.Fatal(err)
	}
	has := func(table, column string) bool {
		var n int
		db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&n)
		return n > 0
	}
	if err := migrateLibrary(db); err == nil {
		t.Fatal("the cut did not stop the migration")
	}
	if err := migrateAPI(db); err == nil {
		t.Fatal("the cut did not stop the API migration")
	}
	if err := migrateThemes(db); err == nil {
		t.Fatal("the cut did not stop the theme migration")
	}
	if has("pages", "opened_at") || has("pages", "updated_at") || has("theme_preferences", "version") {
		t.Fatal("a column survived without its values")
	}
	db.Exec(`DROP TRIGGER cut; DROP TRIGGER cut_themes`)
	for range 2 {
		for _, step := range []func(*sql.DB) error{migratePages, migrateLibrary, migrateAPI, migrateThemes} {
			if err := step(db); err != nil {
				t.Fatal(err)
			}
		}
	}
	var opened, updated, reader string
	var version int
	db.QueryRow(`SELECT opened_at,updated_at FROM pages WHERE id='p'`).Scan(&opened, &updated)
	db.QueryRow(`SELECT reader,version FROM theme_preferences`).Scan(&reader, &version)
	if opened != "2026-01-02T03:04:05Z" || updated != opened || reader != "" || version != 1 {
		t.Errorf("after the rerun: opened %q, updated %q, reader %q, version %d", opened, updated, reader, version)
	}
}

// Finding 10: unlock waits grow per address, the shared part stays short,
// memory is bounded and bcrypt runs outside the library lock.
func TestUnlockBackoff(t *testing.T) {
	l := newUnlockLimiter()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return clock }
	// The first wrong password costs nothing (p9), then waits double from 1s.
	for i, want := range []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second} {
		l.fail("p", "attacker", true)
		if got := l.wait("p", "attacker"); got != want {
			t.Errorf("failure %d: wait %v, want %v", i+1, got, want)
		}
	}
	for range 30 {
		l.fail("p", "attacker", true)
	}
	if got := l.wait("p", "attacker"); got != unlockClientCap {
		t.Errorf("attacker cap: %v", got)
	}
	if got := l.wait("p", "visitor"); got != unlockSharedCap {
		t.Errorf("another visitor waits %v, want at most %v", got, unlockSharedCap)
	}
	if got := l.wait("other-page", "visitor"); got != 0 {
		t.Errorf("another page waits %v", got)
	}
	for range 30 {
		l.fail("q", "local", false)
	}
	if got := l.wait("q", "local"); got != unlockSharedCap {
		t.Errorf("an unknown address waits %v", got)
	}
	clock = clock.Add(unlockQuiet + time.Second)
	if got := l.wait("p", "attacker"); got != 0 {
		t.Errorf("quiet period not forgotten: %v", got)
	}
	l.fail("p", "visitor", true)
	l.succeed("p", "visitor")
	if l.clients["p\x00visitor"] != nil {
		t.Error("success kept the address's failures")
	}
	for i := range unlockMaxEntries + 500 {
		l.fail("p", strconv.Itoa(i), true)
	}
	if len(l.clients) > unlockMaxEntries {
		t.Errorf("limiter holds %d entries", len(l.clients))
	}
	l.forget("p")
	if len(l.pages) != 1 || l.pages["q"] == nil {
		t.Error("forget left the page or dropped another")
	}
	for _, tc := range []struct{ remote, forwarded, bucket string }{
		{"203.0.113.5:1234", "9.9.9.9", "203.0.113.5"},
		{"[2001:db8::1]:1234", "", "2001:db8::/64"},
		{"127.0.0.1:1234", "6.6.6.6, 198.51.100.7", "198.51.100.7"},
		{"127.0.0.1:1234", "", "local"},
		{"[::1]:1234", "[2001:db8::9]:443", "2001:db8::/64"},
	} {
		r := httptest.NewRequest("POST", "/x", nil)
		r.RemoteAddr = tc.remote
		if tc.forwarded != "" {
			r.Header.Set("X-Forwarded-For", tc.forwarded)
		}
		if got, _ := clientBucket(r); got != tc.bucket {
			t.Errorf("bucket for %s via %q: %q", tc.remote, tc.forwarded, got)
		}
	}
}

func TestUnlockRunsOutsideTheLibraryLock(t *testing.T) {
	a := secStart(t, "", nil)
	a.publish(t, "POST", "/api/pages", fxPage("locked", "Locked", "Request", "Body."))
	a.form(t, "/manage/locked", url.Values{"action": {"share"}})
	slow, _ := bcrypt.GenerateFromPassword([]byte("correct horse"), 13)
	a.s.db.Exec(`UPDATE pages SET password_hash=? WHERE slug='locked'`, string(slow))
	done := make(chan int)
	go func() {
		status, _, _ := a.send(t, "POST", "public:/locked", "", strings.NewReader("password=wrong+guess"), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": a.public.URL})
		done <- status
	}()
	time.Sleep(60 * time.Millisecond)
	started := time.Now()
	a.s.mu.Lock()
	waited := time.Since(started)
	a.s.mu.Unlock()
	if status := <-done; status != http.StatusUnauthorized {
		t.Fatalf("unlock: %d", status)
	}
	if waited > 150*time.Millisecond {
		t.Errorf("a writer waited %v for bcrypt", waited)
	}
	// A visitor guessing from another address waits at most the shared cap.
	status, header, _ := a.send(t, "POST", "public:/locked", "", strings.NewReader("password=again"), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": a.public.URL, "X-Forwarded-For": "198.51.100.7"})
	if retry, _ := strconv.Atoi(header.Get("Retry-After")); status == http.StatusTooManyRequests && retry > int(unlockSharedCap/time.Second) {
		t.Errorf("another visitor told to wait %ds", retry)
	}
}

func TestPasswordsOfUnknownLengthAreNoted(t *testing.T) {
	a := secStart(t, "", nil)
	a.publish(t, "POST", "/api/pages", fxPage("p", "Page", "Request", "Body."))
	a.form(t, "/manage/p", url.Values{"action": {"share"}})
	short, _ := bcrypt.GenerateFromPassword([]byte("abc"), bcrypt.MinCost)
	a.s.db.Exec(`UPDATE pages SET password_hash=? WHERE slug='p'`, string(short))
	p, _ := a.s.getPage("p")
	d, err := a.s.manageState(p)
	if err != nil || d.PasswordNote == "" {
		t.Fatalf("legacy password not noted: %v %+v", err, d.PasswordNote)
	}
	if status, _, body := a.form(t, "/manage/p", url.Values{"action": {"password"}, "password": {"a long passphrase"}}); status != 303 {
		t.Fatal(status, body)
	}
	if d, _ = a.s.manageState(p); d.PasswordNote != "" || !d.Protected {
		t.Error("a checked password is still noted")
	}
	// An older Cairn changing the hash makes it unchecked again.
	a.s.db.Exec(`UPDATE pages SET password_hash=? WHERE slug='p'`, string(short))
	if d, _ = a.s.manageState(p); d.PasswordNote == "" {
		t.Error("a hash changed elsewhere kept the checked mark")
	}
}

// Finding 11: no agent key reaches the owner's own pages.
func TestNoAgentKeyForTheOwnersOwnName(t *testing.T) {
	a := secStart(t, "", func(c *Config) { c.Owner = "Chris" })
	for _, name := range []string{"Chris", "chris", " CHRIS "} {
		status, header, body := a.form(t, "/settings", url.Values{"action": {"key_create"}, "name": {name}})
		if status != 400 || !strings.HasPrefix(header.Get("Content-Type"), "text/plain") || !strings.Contains(body, "your own name") {
			t.Errorf("key for %q: %d %s", name, status, body)
		}
	}
	// A key made before the rule can be revoked, not rotated.
	a.s.db.Exec(`INSERT INTO agent_keys(name,digest,created_at) VALUES('Chris',x'00','2026-01-01T00:00:00Z')`)
	if status, _, _ := a.form(t, "/settings", url.Values{"action": {"key_rotate"}, "name": {"Chris"}}); status != 400 {
		t.Errorf("rotate on the owner's name: %d", status)
	}
	if status, _, body := a.form(t, "/settings", url.Values{"action": {"key_revoke"}, "name": {"Chris"}}); status != 200 {
		t.Errorf("revoke on the owner's name: %d %s", status, body)
	}
	newAgentKey(t, a, "Claude")
	status, _, body := a.form(t, "/settings", url.Values{"action": {"defaults"}, "author": {"claude"}, "template": {"report"}, "link_length": {"3"}})
	if status != 400 || !strings.Contains(body, "An agent key already uses this name") || a.s.preferences().Author != "Chris" {
		t.Errorf("default author on a key's name: %d", status)
	}
}

// Finding 14: the runtime serves its three scripts and nothing else.
func TestRuntimeServesOnlyItsScripts(t *testing.T) {
	a := secStart(t, "", nil)
	for _, prefix := range []string{"", "public:"} {
		for _, path := range []string{"/_runtime/", "/_runtime", "/_runtime/MERMAID-LICENSE", "/_runtime/ECHARTS-NOTICE", "/_runtime/nope.js", "/_runtime/../boot.js", "/_ui/nope.css", "/_ui/"} {
			status, header, body := a.send(t, "GET", prefix+path, "", nil, nil)
			if status != 404 || !strings.Contains(body, "<main") || strings.Contains(body, "mermaid.js") || strings.Contains(body, "Permission is hereby granted") || !strings.Contains(header.Get("Content-Security-Policy"), "default-src 'none'") || header.Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s%s: %d %s %.120s", prefix, path, status, header.Get("Content-Type"), body)
			}
		}
		if status, _, body := a.send(t, "POST", prefix+"/_runtime/boot.js", "", nil, nil); status == 200 || !strings.Contains(body, "<main") {
			t.Errorf("%sPOST runtime: %d %.80s", prefix, status, body)
		}
		status, header, _ := a.send(t, "GET", prefix+"/_runtime/boot.js", "", nil, nil)
		if status != 200 || !strings.HasPrefix(header.Get("Content-Type"), "text/javascript") || header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%sboot.js: %d %v", prefix, status, header)
		}
	}
}

// Finding 13: an edit's browser draft is kept by page id, so a new page that
// reuses a deleted page's link name never opens with the old draft.
func TestEditDraftsAreKeyedByPage(t *testing.T) {
	a := secStart(t, "", nil)
	a.publish(t, "POST", "/api/pages", fxPage("lamp", "Lamp", "Request", "Body."))
	first, _ := a.s.getPage("lamp")
	if _, body := a.get(t, "/edit/lamp"); !strings.Contains(body, `data-draft-key="page:`+first.ID+`"`) {
		t.Fatal("the edit form has no page draft key")
	}
	if _, body := a.get(t, "/new"); strings.Contains(body, "data-draft-key") {
		t.Error("a new page has a page draft key")
	}
	a.form(t, "/manage/lamp/delete", url.Values{"action": {"delete"}})
	a.publish(t, "POST", "/api/pages", fxPage("lamp", "Another lamp", "Request", "Body."))
	second, _ := a.s.getPage("lamp")
	if _, body := a.get(t, "/edit/lamp"); second.ID == first.ID || !strings.Contains(body, `data-draft-key="page:`+second.ID+`"`) {
		t.Error("a reused link name shares the old page's draft key")
	}
	// The cookie that tells the editor to forget drafts names recent
	// deletions, newest first, and nothing else.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/manage/x/delete", nil)
	req.AddCookie(&http.Cookie{Name: "cairn_deleted", Value: strings.Repeat("a", 32) + ".bad." + strings.Repeat("b", 32)})
	forgetDraft(rec, req, strings.Repeat("c", 32))
	if got := rec.Header().Get("Set-Cookie"); !strings.HasPrefix(got, "cairn_deleted="+strings.Repeat("c", 32)+"."+strings.Repeat("a", 32)+"."+strings.Repeat("b", 32)+";") {
		t.Errorf("cookie: %s", got)
	}
}
