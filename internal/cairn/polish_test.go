package cairn_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var scriptTag = regexp.MustCompile(`<script[^>]*>`)

// Page scripts are files under /_ui/ with content-hashed names, cached for a
// year like the stylesheet, and the policy allows only Cairn's own files.
func TestPageScriptsAreCachedFiles(t *testing.T) {
	private, public, closeApp := openThemed(t, t.TempDir())
	defer closeApp()
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Shared"}, "markdown": {"# Shared\n\nText."}, "slug": {"shared"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	if code := ownerPost(t, private, "shared", url.Values{"action": {"password"}, "password": {"secret-words"}}); code != 303 {
		t.Fatal(code)
	}
	scripts := map[string][]byte{}
	check := func(server *httptest.Server, path, name string) {
		t.Helper()
		code, h, body := request(t, server, "GET", path, nil, "")
		tags := scriptTag.FindAllString(string(body), -1)
		want := regexp.MustCompile(`^<script src="/_ui/` + name + `\.[0-9a-f]{12}\.js">$`)
		if code >= 500 || len(tags) != 1 || !want.MatchString(tags[0]) {
			t.Fatalf("%s: scripts %v", path, tags)
		}
		policy := h.Get("Content-Security-Policy")
		if !strings.Contains(policy, "script-src 'self'") || strings.Contains(policy, "sha256-") || strings.Contains(policy, "unsafe-inline") || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: policy %q", path, policy)
		}
		src := tags[0][len(`<script src="`) : len(tags[0])-2]
		status, sh, js := request(t, server, "GET", src, nil, "")
		if status != 200 || sh.Get("Content-Type") != "text/javascript; charset=utf-8" || sh.Get("Cache-Control") != "public, max-age=31536000, immutable" || len(js) == 0 {
			t.Fatalf("%s: script %s answered %d %v", path, src, status, sh)
		}
		if seen, ok := scripts[src]; ok && !bytes.Equal(seen, js) {
			t.Fatalf("%s differs between listeners", src)
		}
		scripts[src] = js
	}
	for path, name := range map[string]string{"/": "library", "/settings": "settings", "/new": "editor", "/edit/shared": "editor", "/import": "forms", "/manage/shared": "forms", "/shared": "reader", "/no-such-page": "focus"} {
		check(private, path, name)
	}
	for path, name := range map[string]string{"/shared": "forms", "/no-such-page": "focus"} {
		check(public, path, name)
	}
	// Every script starts with the focus script, so every page has its keyboard rings and dates.
	for src, js := range scripts {
		if !strings.Contains(string(js), "inputModality") || !strings.Contains(string(js), "cairnDate") {
			t.Errorf("%s lacks the shared script", src)
		}
	}
	// Compressed for browsers that take gzip.
	src := firstKey(scripts)
	req, _ := http.NewRequest("GET", private.URL+src, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	zipped, _ := io.ReadAll(res.Body)
	res.Body.Close()
	unzipped, err := gzip.NewReader(bytes.NewReader(zipped))
	if err != nil || res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("script not compressed", err)
	}
	plain, _ := io.ReadAll(unzipped)
	if !bytes.Equal(plain, scripts[src]) {
		t.Fatal("compressed script differs")
	}
	// An empty library no longer carries its script in the page.
	empty, _, closeEmpty := openThemed(t, t.TempDir())
	defer closeEmpty()
	if _, _, body := request(t, empty, "GET", "/", nil, ""); len(body) > 12000 {
		t.Fatalf("empty library is %d bytes", len(body))
	}
}

func firstKey(m map[string][]byte) string {
	for k := range m {
		return k
	}
	return ""
}

// Deleting a page in Manage also forgets the stored answers to keyed API
// requests that wrote it: they hold its original request.
func TestDeletingAPageForgetsItsStoredReplies(t *testing.T) {
	dir := t.TempDir()
	srv, closeApp := start(t, dir)
	defer closeApp()
	publish := func(slug, key string) {
		t.Helper()
		in := page("Page " + slug)
		in["slug"] = slug
		in["original_request"] = "PRIVATE REQUEST " + slug
		body, _ := json.Marshal(in)
		req, _ := http.NewRequest("POST", srv.URL+"/api/pages", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", key)
		res, err := srv.Client().Do(req)
		if err != nil || res.StatusCode != 201 {
			t.Fatal(err, res.StatusCode)
		}
		res.Body.Close()
	}
	publish("gone", "key-gone")
	publish("kept", "key-kept")
	db := rawDB(t, dir)
	defer db.Close()
	// Stored replies name their page and never hold its request.
	held := func(key string) int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE key=? AND page_id!=''`, key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var leaked int
	db.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE CAST(response AS TEXT) LIKE '%PRIVATE REQUEST%'`).Scan(&leaked)
	if held("key-gone") != 1 || held("key-kept") != 1 || leaked != 0 {
		t.Fatalf("keyed replies: %d %d, %d holding a request", held("key-gone"), held("key-kept"), leaked)
	}
	if code := ownerPost(t, srv, "gone/delete", url.Values{"action": {"delete"}}); code != 303 {
		t.Fatal(code)
	}
	if held("key-gone") != 0 {
		t.Fatal("deleted page's reply still stored")
	}
	if held("key-kept") != 1 {
		t.Fatal("another page's reply was dropped")
	}
}

// Renaming, archiving and restoring in Manage or from the library change
// updated_at, as the API's own changes do.
func TestManageChangesStampUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	srv, closeApp := start(t, dir)
	defer closeApp()
	if code, _, body := request(t, srv, "POST", "/api/pages", page("Bulbs"), token); code != 201 {
		t.Fatal(code, string(body))
	}
	db := rawDB(t, dir)
	defer db.Close()
	updated := func() string {
		var v string
		if err := db.QueryRow(`SELECT updated_at FROM pages WHERE slug='report'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, change := range []url.Values{{"action": {"rename"}, "title": {"Bulbs, renamed"}}, {"action": {"archive"}}, {"action": {"restore"}}} {
		if _, err := db.Exec(`UPDATE pages SET updated_at='2001-01-01T00:00:00Z' WHERE slug='report'`); err != nil {
			t.Fatal(err)
		}
		if code := ownerPost(t, srv, "report", change); code != 303 {
			t.Fatal(change, code)
		}
		if got := updated(); got <= "2001-01-01T00:00:00Z" {
			t.Fatalf("%s left updated_at at %s", change.Get("action"), got)
		}
	}
	var out struct {
		UpdatedAt string `json:"updated_at"`
	}
	_, _, body := request(t, srv, "GET", "/api/pages/report", nil, token)
	if json.Unmarshal(body, &out) != nil || out.UpdatedAt <= "2001-01-01T00:00:00Z" {
		t.Fatal("API does not report the change", string(body))
	}
}

// Previous and next follow the list the owner came from: a search, an author
// or an order, and Back returns to it.
func TestReaderFollowsTheLibraryViewItCameFrom(t *testing.T) {
	srv, closeApp := start(t, t.TempDir())
	defer closeApp()
	for i, p := range []struct{ slug, agent string }{{"a1", "Ada"}, {"b1", "Bo"}, {"a2", "Ada"}, {"b2", "Bo"}, {"a3", "Ada"}} {
		in := page("Page " + p.slug)
		in["slug"], in["agent"] = p.slug, p.agent
		in["html"] = "<h1>Page</h1><p>Text " + strings.Repeat("x", i) + "</p>"
		if code, _, body := request(t, srv, "POST", "/api/pages", in, token); code != 201 {
			t.Fatal(code, string(body))
		}
	}
	_, _, list := request(t, srv, "GET", "/?agent=Ada", nil, "")
	if !strings.Contains(string(list), `href="/a2?in=agent%3DAda" data-preview`) {
		t.Fatal("rows do not carry the view")
	}
	_, _, plain := request(t, srv, "GET", "/", nil, "")
	if strings.Contains(string(plain), "?in=") {
		t.Fatal("the plain library needs no view")
	}
	_, _, reader := request(t, srv, "GET", "/a2?in=agent%3DAda", nil, "")
	page := string(reader)
	for _, want := range []string{`href="/a3?in=agent%3DAda" rel="prev"`, `href="/a1?in=agent%3DAda" rel="next"`, `<a class="end-back" href="/?agent=Ada">`, `name="return" value="/?agent=Ada"`} {
		if !strings.Contains(page, want) {
			t.Errorf("reader lacks %s", want)
		}
	}
	// Without a view, or with one the page has left, the library's order holds.
	for _, path := range []string{"/a2", "/a2?in=agent%3DBo", "/a2?in=%%"} {
		_, _, body := request(t, srv, "GET", path, nil, "")
		if !strings.Contains(string(body), `href="/b2" rel="prev"`) || !strings.Contains(string(body), `href="/b1" rel="next"`) {
			t.Errorf("%s: neighbours should follow the library", path)
		}
	}
}

// Dates carry their instant so the browser can write them in its own time
// zone; the server's words stay as the fallback.
func TestDatesCarryTheirInstantForTheBrowser(t *testing.T) {
	srv, closeApp := start(t, t.TempDir())
	defer closeApp()
	if code, _, _ := request(t, srv, "POST", "/api/pages", page("Bulbs"), token); code != 201 {
		t.Fatal(code)
	}
	_, _, list := request(t, srv, "GET", "/", nil, "")
	if !regexp.MustCompile(`<time class="date" datetime="[0-9T:Z-]+" title="[^"]+ at \d\d:\d\d" data-local="relative">Just now`).Match(list) {
		t.Fatal("library date not marked for the browser")
	}
	_, _, reader := request(t, srv, "GET", "/report", nil, "")
	if !regexp.MustCompile(`<time datetime="[0-9T:Z-]+" data-local="day">\d+ \w{3} \d{4}</time>`).Match(reader) {
		t.Fatal("reader date not marked for the browser")
	}
}

// Fields are 17px on touch screens, where iOS zooms into anything under 16px,
// and stay at the body size with a mouse.
func TestTouchFieldsDoNotZoom(t *testing.T) {
	tokens, _ := os.ReadFile(filepath.Join("ui", "tokens.css"))
	base, _ := os.ReadFile(filepath.Join("ui", "base.css"))
	coarse := string(tokens)[strings.Index(string(tokens), "@media (pointer: coarse)"):]
	coarse = coarse[:strings.Index(coarse, "}\n}")]
	if !strings.Contains(coarse, "--text-field: var(--text-lead);") || !strings.Contains(string(tokens), "--text-field: var(--text-body);") {
		t.Fatal("field size tokens")
	}
	if !strings.Contains(string(base), "input,\nselect,\ntextarea {\n  font-size: var(--text-field);") {
		t.Fatal("fields do not use the field size")
	}
}
