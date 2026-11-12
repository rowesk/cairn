package cairn

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Finding and editing: search, excerpts, the list, unread pages and the
// edit route.

const fxToken = "finding-test-token-with-at-least-32-characters"

type fxApp struct {
	s               *Server
	private, public *httptest.Server
	dir             string
}

func fxStart(t *testing.T, dir string) *fxApp {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	s, err := Open(Config{DataDir: dir, PublisherToken: fxToken, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	s.waitIndexed()
	a := &fxApp{s: s, private: httptest.NewServer(s), public: httptest.NewServer(s.PublicHandler()), dir: dir}
	t.Cleanup(a.close)
	return a
}

func (a *fxApp) close() {
	if a.private == nil {
		return
	}
	a.private.Close()
	a.public.Close()
	a.s.Close()
	a.private = nil
}

func (a *fxApp) do(t *testing.T, method, path string, body io.Reader, header map[string]string) (int, http.Header, string) {
	t.Helper()
	base := a.private.URL
	if strings.HasPrefix(path, "public:") {
		base, path = a.public.URL, strings.TrimPrefix(path, "public:")
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
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

func (a *fxApp) get(t *testing.T, path string) (int, string) {
	t.Helper()
	status, _, body := a.do(t, "GET", path, nil, nil)
	return status, body
}

func (a *fxApp) publish(t *testing.T, method, path string, page map[string]any) {
	t.Helper()
	data, _ := json.Marshal(page)
	status, _, body := a.do(t, method, path, bytes.NewReader(data), map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + fxToken})
	if status != 201 && status != 200 {
		t.Fatalf("%s %s: %d %s", method, path, status, body)
	}
}

func (a *fxApp) form(t *testing.T, path string, values url.Values) (int, http.Header, string) {
	t.Helper()
	if !values.Has("csrf") {
		values.Set("csrf", a.s.formToken().String())
	}
	return a.do(t, "POST", path, strings.NewReader(values.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": a.private.URL})
}

func fxPage(slug, title, request, markdown string) map[string]any {
	return map[string]any{"slug": slug, "title": title, "agent": "Codex", "original_request": request, "markdown": markdown}
}

// rowsOf lists the slugs of the library rows in order.
func rowsOf(body string) []string {
	var slugs []string
	for _, m := range regexp.MustCompile(`<article class="report-row[^"]*" id="page-row-\d+" data-slug="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		slugs = append(slugs, m[1])
	}
	return slugs
}

func TestSearchFindsBodyWordsAndFollowsEveryChange(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("filter", "Water filter notes", "Which jug filter should I buy", "# Water filter notes\n\nThe jug uses a zeolite cartridge that lasts six months."))
	a.publish(t, "POST", "/api/pages", fxPage("other", "Garden shed rules", "Do I need planning permission", "A shed under 2.5 metres needs no permission."))

	_, body := a.get(t, "/?q=zeol")
	if got := rowsOf(body); len(got) != 1 || got[0] != "filter" {
		t.Fatalf("prefix body search found %v", got)
	}
	if !strings.Contains(body, `<span class="where">In the page:</span>`) || !strings.Contains(body, "<mark>zeolite</mark>") {
		t.Fatalf("body match has no labelled, highlighted snippet: %s", body)
	}

	// Replacement re-indexes: the old word goes, the new one is found.
	a.publish(t, "PUT", "/api/pages/filter", fxPage("filter", "Water filter notes", "Which jug filter should I buy", "The jug now uses a carbon block."))
	if _, body := a.get(t, "/?q=zeolite"); len(rowsOf(body)) != 0 {
		t.Error("replaced page still matches its old words")
	}
	if _, body := a.get(t, "/?q=carbon"); len(rowsOf(body)) != 1 {
		t.Error("replaced page does not match its new words")
	}

	// Rename re-indexes the title.
	if status, _, _ := a.form(t, "/manage/filter", url.Values{"action": {"rename"}, "title": {"Kitchen tap purifier"}}); status != 303 {
		t.Fatal(status)
	}
	_, body = a.get(t, "/?q=purif")
	if got := rowsOf(body); len(got) != 1 || !strings.Contains(body, "<mark>Purifier</mark>") && !strings.Contains(body, "<mark>purifier</mark>") {
		t.Errorf("renamed title not found or not highlighted: %v", got)
	}

	// Delete removes the index row.
	if status, _, _ := a.form(t, "/manage/filter/delete", url.Values{"action": {"delete"}}); status != 303 {
		t.Fatal(status)
	}
	if _, body := a.get(t, "/?q=carbon"); len(rowsOf(body)) != 0 {
		t.Error("deleted page still found")
	}
	var left int
	a.s.db.QueryRow(`SELECT count(*) FROM pages_fts`).Scan(&left)
	if left != 1 {
		t.Errorf("index holds %d rows, want 1", left)
	}
}

func TestSearchRanksTitleThenRequestThenBody(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("body", "Kitchen plans", "Plan the kitchen", "We picked the walnut worktop in the end. "+strings.Repeat("Walnut walnut walnut. ", 20)))
	a.publish(t, "POST", "/api/pages", fxPage("request", "Worktop choices", "Compare walnut and oak worktops", "Oak is cheaper."))
	a.publish(t, "POST", "/api/pages", fxPage("title", "Walnut worktop care", "How do I look after it", "Oil it twice a year."))
	_, body := a.get(t, "/?q=walnut")
	if got := strings.Join(rowsOf(body), ","); got != "title,request,body" {
		t.Fatalf("ranking %s, want title,request,body", got)
	}
	if !strings.Contains(body, `<span class="where">In your request:</span>`) {
		t.Error("request match does not say where it was found")
	}
	// A chosen order replaces ranking.
	_, body = a.get(t, "/?q=walnut&sort=oldest")
	if got := strings.Join(rowsOf(body), ","); got != "body,request,title" {
		t.Errorf("oldest first gave %s", got)
	}
	// Accents and case do not matter.
	a.publish(t, "POST", "/api/pages", fxPage("cafe", "Café crème recipe", "How strong", "Use a double shot."))
	if _, body := a.get(t, "/?q=CAFE"); strings.Join(rowsOf(body), ",") != "cafe" {
		t.Error("search is not diacritic and case insensitive")
	}
	if _, body := a.get(t, "/?q=%22double+shot%22"); strings.Join(rowsOf(body), ",") != "cafe" {
		t.Error("quoted phrase did not match")
	}
}

func TestSnippetsAndHighlightsAreHTMLSafe(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", map[string]any{"slug": "x", "title": `Bulbs <img src=x onerror=alert(1)> guide`, "agent": "Codex", "original_request": `Ask <script>alert(2)</script> about bulbs`, "html": `<p>Text with &lt;script&gt;alert(3)&lt;/script&gt; and bulbs inside.</p>`})
	for _, q := range []string{"bulbs", "img", "script", "alert"} {
		_, body := a.get(t, "/?q="+q)
		rows := body[strings.Index(body, `<div class="rows"`):]
		if strings.Contains(rows, "<img src=x") || strings.Contains(rows, "<script>alert") {
			t.Fatalf("query %q put live markup in the list: %s", q, rows)
		}
		if !strings.Contains(rows, "<mark>") {
			t.Errorf("query %q highlighted nothing", q)
		}
	}
	if marked("a"+markOpen+"<b>"+markClose+"c") != "a<mark>&lt;b&gt;</mark>c" {
		t.Error("marked escapes after marking")
	}
	if got := matchedLines("…Commands\nzfs snapshot " + markOpen + "nightly" + markClose + "\nWhat to do next\nCheck it…"); got != "…zfs snapshot "+markOpen+"nightly"+markClose+"…" {
		t.Errorf("snippet lines: %q", got)
	}
}

func TestHostileSearchTextNeverBreaksTheList(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("p", "Plain page", "Request", "Body words."))
	for _, q := range []string{`"`, `"""`, `*`, `NEAR(a b)`, `a OR`, `-plain`, `^plain`, `title:plain`, `plain AND`, `(plain`, `plain)`, `{title}: x`, `%`, "\x00", `'; DROP TABLE pages; --`, `🙂`, strings.Repeat("plain ", 2000), "plain", `" OR "1"="1`, `\`, `plain*`} {
		status, body := a.get(t, "/?q="+url.QueryEscape(q))
		if status != 200 || !strings.Contains(body, `class="sheet"`) {
			t.Fatalf("query %q: %d", q, status)
		}
		if status, _ := a.get(t, "/?fragment=list&q="+url.QueryEscape(q)); status != 200 {
			t.Fatalf("fragment query %q: %d", q, status)
		}
	}
	if terms := searchTerms(`"exact phrase" word* -x OR`); fmt.Sprint(terms) != "[[exact phrase] [word] [x] [OR]]" {
		t.Errorf("terms %v", terms)
	}
	if ftsQuery(searchTerms(`a"b`)) != `"a"* "b"*` {
		t.Errorf("quote inside a word: %s", ftsQuery(searchTerms(`a"b`)))
	}
	if _, body := a.get(t, "/?q="+url.QueryEscape(`!!!`)); !strings.Contains(body, "No pages match “!!!”") {
		t.Error("a query with no words should say nothing matched")
	}
}

func TestLibraryPagesFiftyAtATime(t *testing.T) {
	a := fxStart(t, "")
	for i := 0; i < 120; i++ {
		a.publish(t, "POST", "/api/pages", map[string]any{"slug": fmt.Sprintf("p%03d", i), "title": fmt.Sprintf("Page %03d", i), "agent": "Codex", "original_request": strings.Repeat("A long private request. ", 200), "html": "<p>Body text for the list.</p>"})
	}
	_, body := a.get(t, "/")
	if n := len(rowsOf(body)); n != 50 {
		t.Fatalf("first page has %d rows", n)
	}
	if !strings.Contains(body, `href="/?n=100#page-row-51" data-offset="50">Show more pages</a>`) || !strings.Contains(body, "50 of 120 pages") {
		t.Error("no working Show more link or count")
	}
	if n := strings.Count(body, "A long private request."); n > 50*7 {
		t.Errorf("rows carry whole original requests: %d copies", n)
	}
	if strings.Count(body, " popover") > 1 {
		t.Error("each row has its own menu")
	}
	_, body = a.get(t, "/?n=100")
	if n := len(rowsOf(body)); n != 100 || !strings.Contains(body, `id="page-row-51"`) {
		t.Errorf("n=100 shows %d rows", n)
	}
	_, body = a.get(t, "/?fragment=rows&offset=50")
	if rows := rowsOf(body); len(rows) != 50 || rows[0] != "p069" || !strings.Contains(body, `data-offset="100">Show more pages</a>`) || strings.Contains(body, "<html") {
		t.Errorf("middle rows fragment: %v", rows)
	}
	_, body = a.get(t, "/?fragment=rows&offset=100")
	if rows := rowsOf(body); len(rows) != 20 || rows[0] != "p019" || strings.Contains(body, "Show more") || strings.Contains(body, "<html") {
		t.Errorf("last rows fragment: %v", rows)
	}
	if _, header, _ := a.do(t, "GET", "/", nil, nil); !strings.Contains(header.Get("Content-Security-Policy"), "img-src 'self' data:") || !strings.Contains(header.Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Error("list policy blocks avatars or its own fetches")
	}
	_, body = a.get(t, "/?fragment=list&q=page")
	if strings.Contains(body, "<html") || len(rowsOf(body)) != 50 {
		t.Error("list fragment is not a fragment")
	}
}

func TestExcerptIsTheOpeningParagraph(t *testing.T) {
	html, _ := renderFixture(t, "report")
	excerpt, body := pageText(html, "Bulbs for the bedroom")
	want := "A warm LED is the right bulb for a bedroom lamp. It uses a seventh of the power of a halogen and gives a softer light late at night."
	if excerpt != want {
		t.Fatalf("excerpt\n got %q\nwant %q", excerpt, want)
	}
	if !strings.Contains(body, "Warm LED · 6 W · 800 lm · £4.50") {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "Warm LED") {
				t.Logf("line: %q", line)
			}
		}
		t.Error("table cells run together in the search text")
	}
	for _, leak := range []string{"flowchart", "-->", "xAxis", "\"series\""} {
		if strings.Contains(body, leak) {
			t.Errorf("search text holds diagram or chart syntax %q", leak)
		}
	}
	cases := map[string]string{
		`<h1 class="title-echo">Monitors</h1><table><tr><td>Model</td><td>Price</td></tr></table><p>The Dell is the one to buy for this desk and budget.</p>`: "The Dell is the one to buy for this desk and budget.",
		`<h1>Notes</h1><pre><code class="language-mermaid">flowchart LR
 A[Home server] --> B[Snapshot]</code></pre><p>Snapshots run nightly at two in the morning.</p>`: "Snapshots run nightly at two in the morning.",
		`<p>Updated 3 June</p><p>The roof needs new felt before the winter storms arrive.</p>`:                                   "The roof needs new felt before the winter storms arrive.",
		`<p>A short opening line here.</p><p>It continues here.</p><h2>Next</h2><p>Another section entirely.</p>`:                "A short opening line here. It continues here.",
		`<nav><p>Skip to the end of the navigation here please</p></nav><main><p>Real opening paragraph of the page.</p></main>`: "Real opening paragraph of the page.",
		`<pre><code>plain text file contents</code></pre>`:                                                                       "plain text file contents",
		`<img src="x.png" alt="A picture">`:                                                                                      "",
	}
	for source, want := range cases {
		if got, _ := pageText(source, "Monitors"); got != want {
			t.Errorf("pageText(%q)\n got %q\nwant %q", source, got, want)
		}
	}
	if got := diagramLabels("flowchart LR\n A[Home server] --> B[(Snapshot)]\n B -->|nightly| C{Drive}"); got != "Home server · Snapshot · nightly · Drive" {
		t.Errorf("diagram labels %q", got)
	}
}

func TestDerivedDataIsRebuiltInTheBackground(t *testing.T) {
	dir := t.TempDir()
	a := fxStart(t, dir)
	a.publish(t, "POST", "/api/pages", fxPage("old", "Old page", "Old request", "# Old page\n\nThe obsidian lamp is the one to keep."))
	a.close()
	// An older Cairn: no index rows, an excerpt that repeats the title.
	db := openRaw(t, dir)
	db.Exec(`DELETE FROM pages_fts`)
	db.Exec(`UPDATE pages SET index_version=0, preview='Old page The obsidian lamp', source_name=''`)
	db.Close()

	s, err := Open(Config{DataDir: dir, PublisherToken: fxToken})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.waitIndexed()
	items, _, err := s.listPages(libraryQuery{Text: "obsid", Limit: 50})
	if err != nil || len(items) != 1 || items[0].Preview != "The obsidian lamp is the one to keep." {
		t.Fatalf("rebuilt page: %+v %v", items, err)
	}
	// While a page waits for the rebuild, its title or request still match.
	s.db.Exec(`DELETE FROM pages_fts`)
	s.db.Exec(`UPDATE pages SET index_version=0`)
	s.indexing.Store(true)
	items, total, err := s.listPages(libraryQuery{Text: "Old request", Limit: 50})
	s.indexing.Store(false)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("fallback search during rebuild: %v %d %v", items, total, err)
	}
	if more, err := s.indexBatch(25); err != nil || more {
		t.Fatal(more, err)
	}
	if items, _, _ := s.listPages(libraryQuery{Text: "obsidian", Limit: 50}); len(items) != 1 || items[0].Snippet == "" {
		t.Error("a batch did not index the page")
	}
}

func TestPreviewRequestEndpointIsOwnerOnly(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("p", "Private page", "PRIVATE-REQUEST-WORDS", "Body."))
	if status, _, _ := a.do(t, "GET", "/_library/request/p", nil, nil); status != 403 {
		t.Errorf("no token: %d", status)
	}
	if status, _, _ := a.do(t, "GET", "/_library/request/p", nil, map[string]string{"X-Cairn-Token": a.s.formToken().String(), "Sec-Fetch-Site": "cross-site"}); status != 403 {
		t.Errorf("cross-site: %d", status)
	}
	status, header, body := a.do(t, "GET", "/_library/request/p", nil, map[string]string{"X-Cairn-Token": a.s.formToken().String(), "Sec-Fetch-Site": "same-origin"})
	if status != 200 || !strings.Contains(body, "PRIVATE-REQUEST-WORDS") || header.Get("Content-Type") != "application/json" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("owner fetch: %d %v %s", status, header, body)
	}
	// Nothing under /_library/ or /edit/ exists on the public listener.
	if status, _, _ := a.form(t, "/manage/p", url.Values{"action": {"share"}}); status != 303 {
		t.Fatal(status)
	}
	for _, path := range []string{"/_library/request/p", "/_library/link-name?name=x", "/_library/avatar?name=Codex", "/edit/p", "/?fragment=list", "/?fragment=rows&offset=0"} {
		status, _, body := a.do(t, "GET", "public:"+path, nil, map[string]string{"X-Cairn-Token": a.s.formToken().String()})
		if status != 404 || strings.Contains(body, "PRIVATE-REQUEST-WORDS") {
			t.Errorf("public %s: %d", path, status)
		}
	}
	if status, _, _ := a.do(t, "POST", "public:/_library/archive", strings.NewReader("slug=p"), nil); status != 404 {
		t.Errorf("public archive: %d", status)
	}
}

func TestLinkNameCheckWhileTyping(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("taken", "Taken", "Request", "Body."))
	check := func(query string) map[string]any {
		_, _, body := a.do(t, "GET", "/_library/link-name?"+query, nil, map[string]string{"X-Cairn-Token": a.s.formToken().String()})
		var v map[string]any
		json.Unmarshal([]byte(body), &v)
		return v
	}
	if v := check("name=taken"); v["ok"] != false || v["message"] != linkNameTaken {
		t.Errorf("taken name: %v", v)
	}
	if v := check("name=taken&page=taken"); v["ok"] != true {
		t.Errorf("own name counted as taken: %v", v)
	}
	if v := check("name=settings"); v["ok"] != false {
		t.Errorf("reserved name accepted: %v", v)
	}
	if v := check("name=fresh&access=shared"); v["ok"] != true || v["base"] != "https://reports.example.com/" {
		t.Errorf("shared base: %v", v)
	}
	if status, _, _ := a.do(t, "GET", "/_library/link-name?name=x", nil, nil); status != 403 {
		t.Error("link name check without the token")
	}
}

func TestUnreadUntilTheOwnerOpensIt(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("fresh", "Fresh page", "Request", "Body."))
	unread := func() bool {
		_, body := a.get(t, "/")
		return strings.Contains(body, `class="report-row is-unread" id="page-row-1" data-slug="fresh"`)
	}
	if !unread() {
		t.Fatal("a new agent page is not marked unread")
	}
	if status, _, _ := a.form(t, "/manage/fresh", url.Values{"action": {"share"}}); status != 303 {
		t.Fatal(status)
	}
	navigate := map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Accept": "text/html,application/xhtml+xml"}
	a.do(t, "GET", "public:/fresh", nil, navigate)
	a.get(t, "public:/_content/fresh")
	a.do(t, "HEAD", "/fresh", nil, navigate)
	// Another website can make the browser fetch the page without anyone
	// seeing it: an image tag, a script tag, its own frame, a prefetch.
	for _, attempt := range []struct{ path, dest, site string }{
		{"/fresh", "image", "cross-site"},
		{"/_content/fresh", "image", "cross-site"},
		{"/_content/fresh", "script", "cross-site"},
		{"/fresh", "iframe", "cross-site"},
		{"/_content/fresh", "iframe", "cross-site"},
		{"/_content/fresh", "document", "cross-site"},
		{"/_content/fresh", "iframe", "same-site"},
	} {
		a.do(t, "GET", attempt.path, nil, map[string]string{"Sec-Fetch-Dest": attempt.dest, "Sec-Fetch-Site": attempt.site, "Accept": "*/*"})
	}
	a.do(t, "GET", "/fresh", nil, map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Site": "cross-site", "Sec-Purpose": "prefetch", "Accept": "text/html"})
	a.do(t, "GET", "/fresh", nil, map[string]string{"Accept": "image/avif,image/webp,*/*"})
	if !unread() {
		t.Error("a visitor, a HEAD request or another website marked the page read")
	}
	a.do(t, "GET", "/_content/fresh", nil, map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "same-origin"})
	if unread() {
		t.Error("the library's preview did not mark the page read")
	}
	a.publish(t, "POST", "/api/pages", fxPage("second", "Second page", "Request", "Body."))
	a.do(t, "GET", "/second", nil, navigate)
	_, body := a.get(t, "/")
	if strings.Contains(body, `data-slug="second"`) && strings.Contains(body, `class="report-row is-unread" id="page-row-1" data-slug="second"`) {
		t.Error("opening the reader did not mark the page read")
	}
	// Pages the owner writes start read.
	values := url.Values{"title": {"Mine"}, "markdown": {"My words."}, "slug": {"mine"}}
	if status, _, body := a.form(t, "/new", values); status != 303 {
		t.Fatal(status, body)
	}
	_, body = a.get(t, "/")
	if strings.Contains(body, `is-unread" id="page-row-1" data-slug="mine"`) {
		t.Error("an owner-written page is unread")
	}
}

func TestOwnerWrittenPagesStoreNoRequest(t *testing.T) {
	a := fxStart(t, "")
	if status, _, body := a.form(t, "/new", url.Values{"title": {"Mine"}, "markdown": {"My words."}, "slug": {"mine"}}); status != 303 {
		t.Fatal(status, body)
	}
	p, err := a.s.getPage("mine")
	if err != nil || p.OriginalRequest != "" {
		t.Fatalf("stored request %q %v", p.OriginalRequest, err)
	}
	_, body := a.get(t, "/mine")
	if strings.Contains(body, "Your request") || strings.Contains(body, "Written in Cairn") {
		t.Error("reader shows a request block for an owner-written page")
	}
	_, body = a.get(t, "/")
	if strings.Contains(body, `class="asked"`) {
		t.Error("library shows an empty request line")
	}
	// The API still needs a request.
	in := fxPage("api", "API page", "", "Body.")
	data, _ := json.Marshal(in)
	if status, _, _ := a.do(t, "POST", "/api/pages", bytes.NewReader(data), map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + fxToken}); status != 400 {
		t.Errorf("API accepted a page with no request: %d", status)
	}
}

func TestOldPlaceholderRequestsAreCleared(t *testing.T) {
	dir := t.TempDir()
	a := fxStart(t, dir)
	a.publish(t, "POST", "/api/pages", fxPage("w", "Written", "Written in Cairn", "Body."))
	a.close()
	db := openRaw(t, dir)
	db.Exec(`PRAGMA user_version=0`)
	db.Close()
	b := fxStart(t, dir)
	if p, _ := b.s.getPage("w"); p.OriginalRequest != "" {
		t.Errorf("placeholder kept: %q", p.OriginalRequest)
	}
}

func TestArchiveFromTheListWithUndo(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("a", "Archive me", "Request", "Body."))
	status, header, _ := a.form(t, "/_library/archive", url.Values{"slug": {"a"}, "archived": {"1"}, "return": {"/?q=archive"}})
	if status != 303 || header.Get("Location") != "/?done=archived&page=a&q=archive" {
		t.Fatalf("archive: %d %s", status, header.Get("Location"))
	}
	_, body := a.get(t, header.Get("Location"))
	if !strings.Contains(body, "Archived “Archive me”. Its links still work.") || !strings.Contains(body, `<input type="hidden" name="archived" value="0">`) || !strings.Contains(body, ">Undo</button>") {
		t.Error("archive notice has no Undo")
	}
	status, _, body = a.do(t, "POST", "/_library/archive", strings.NewReader(url.Values{"csrf": {a.s.formToken().String()}, "slug": {"a"}, "archived": {"0"}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": a.private.URL, "Accept": "application/json"})
	if status != 200 || !strings.Contains(body, `"archived":false`) {
		t.Fatalf("JSON restore: %d %s", status, body)
	}
	if _, body := a.get(t, "/"); len(rowsOf(body)) != 1 {
		t.Error("restore did not bring the page back")
	}
	for name, header := range map[string]map[string]string{
		"foreign origin": {"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://evil.example"},
		"no origin":      {"Content-Type": "application/x-www-form-urlencoded"},
	} {
		if status, _, _ := a.do(t, "POST", "/_library/archive", strings.NewReader(url.Values{"csrf": {a.s.formToken().String()}, "slug": {"a"}, "archived": {"1"}}.Encode()), header); status != 403 {
			t.Errorf("%s: %d", name, status)
		}
	}
	if status, _, _ := a.form(t, "/_library/archive", url.Values{"csrf": {"stale"}, "slug": {"a"}, "archived": {"1"}}); status != 403 {
		t.Error("stale token archived the page")
	}
	if status, header, _ := a.form(t, "/_library/archive", url.Values{"slug": {"a"}, "archived": {"1"}, "return": {"//evil.example/"}}); status != 303 || !strings.HasPrefix(header.Get("Location"), "/?") {
		t.Errorf("open redirect: %s", header.Get("Location"))
	}
}

func TestEditKeepsAddressDateSharingPasswordAndImages(t *testing.T) {
	a := fxStart(t, "")
	png := fixtureImage()
	images, _ := json.Marshal(map[string][]byte{"pasted-1.png": png})
	values := url.Values{"title": {"Lamp notes"}, "markdown": {"# Lamp notes\n\nFirst draft.\n\n![Lamp](?asset=pasted-1.png)"}, "slug": {"lamp"}, "images": {string(images)}, "access": {"public"}}
	if status, _, body := a.form(t, "/new", values); status != 303 {
		t.Fatal(status, body)
	}
	if status, _, _ := a.form(t, "/manage/lamp", url.Values{"action": {"password"}, "password": {"long enough"}}); status != 303 {
		t.Fatal(status)
	}
	before, _ := a.s.getPage("lamp")
	status, body := a.get(t, "/edit/lamp")
	if status != 200 || !strings.Contains(body, "First draft.") || !strings.Contains(body, ">Lamp notes</textarea>") || !strings.Contains(body, `name="access" value="public" checked`) || !strings.Contains(body, "its password") || !strings.Contains(body, `action="/edit/lamp"`) {
		t.Fatalf("edit form: %d %s", status, body)
	}
	if !strings.Contains(body, `data-draft-base="`) || !strings.Contains(body, `<a class="back-link" href="/lamp">`) || !strings.Contains(body, `<span>Lamp notes</span></a>`) {
		t.Error("edit form lacks its draft key or way back")
	}
	// Preview shows the saved image too.
	values = url.Values{"title": {"Lamp notes"}, "markdown": {"# Lamp notes\n\nSecond draft.\n\n![Lamp](?asset=pasted-1.png)"}, "access": {"public"}}
	_, _, preview := a.form(t, "/edit/lamp?preview=1", values)
	if !strings.Contains(preview, "Second draft.") || !strings.Contains(preview, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png)) {
		t.Error("edit preview lost the saved image")
	}
	status, header, _ := a.form(t, "/edit/lamp", values)
	// The reader that opens next confirms the save.
	if status != 303 || header.Get("Location") != "/lamp?saved=1" {
		t.Fatalf("save edit: %d %s", status, header.Get("Location"))
	}
	after, _ := a.s.getPage("lamp")
	if after.Slug != "lamp" || after.CreatedAt != before.CreatedAt || after.Object == before.Object || after.FileName != "page.md" {
		t.Errorf("edit changed identity: %+v -> %+v", before, after)
	}
	if _, _, src := a.do(t, "GET", "/lamp?download=1", nil, nil); !strings.Contains(src, "Second draft.") {
		t.Error("edited Markdown not kept")
	}
	if status, _ := a.get(t, "/_assets/lamp/pasted-1.png"); status != 200 {
		t.Error("edit lost the image")
	}
	if status, _ := a.get(t, "public:/lamp"); status != 401 {
		t.Errorf("edit lost sharing or the password: %d", status)
	}
	if _, body := a.get(t, "/?q=second"); len(rowsOf(body)) != 1 {
		t.Error("edited words not searchable")
	}
	// Switching to Private in the editor stops sharing; the password stays.
	values.Set("access", "private")
	if status, _, body := a.form(t, "/edit/lamp", values); status != 303 {
		t.Fatal(status, body)
	}
	if status, _ := a.get(t, "public:/lamp"); status != 404 {
		t.Error("private edit left the page shared")
	}
	var hash string
	a.s.db.QueryRow(`SELECT coalesce(password_hash,'') FROM pages WHERE slug='lamp'`).Scan(&hash)
	if hash == "" {
		t.Error("edit removed the password")
	}
	// Removing the image reference drops the image.
	values.Set("markdown", "No picture now.")
	a.form(t, "/edit/lamp", values)
	if status, _ := a.get(t, "/_assets/lamp/pasted-1.png"); status != 404 {
		t.Error("an unreferenced image stayed")
	}
	// Errors come back on the form with the writing.
	status, _, body = a.form(t, "/edit/lamp", url.Values{"title": {""}, "markdown": {"Keep this"}, "access": {"private"}})
	if status != 400 || !strings.Contains(body, "Keep this") || !strings.Contains(body, titleMissing) {
		t.Errorf("edit error: %d", status)
	}
	if status, _, _ := a.do(t, "POST", "/edit/lamp", strings.NewReader("title=x&markdown=y"), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://evil.example"}); status != 403 {
		t.Error("foreign edit accepted")
	}
}

func TestEditIsOfferedOnlyWithMarkdownSource(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", map[string]any{"slug": "html", "title": "Custom", "agent": "Codex", "original_request": "R", "html": "<p>Custom HTML</p>"})
	a.publish(t, "POST", "/api/pages", fxPage("md", "Agent Markdown", "R", "Body"))
	// Markdown the API saved before Cairn kept a copy: the page has no source.
	a.publish(t, "POST", "/api/pages", fxPage("old-md", "Older Agent Markdown", "R", "Body"))
	var object string
	a.s.db.QueryRow(`SELECT object FROM pages WHERE slug='old-md'`).Scan(&object)
	for _, name := range []string{"source.json", "source.bin"} {
		if err := os.Remove(filepath.Join(a.s.cfg.DataDir, "objects", object, name)); err != nil {
			t.Fatal(err)
		}
	}
	a.form(t, "/new", url.Values{"title": {"Mine"}, "markdown": {"Mine."}, "slug": {"mine"}})
	for slug, note := range map[string]string{"html": "custom HTML from Codex", "old-md": "sent its Markdown without a copy"} {
		_, body := a.get(t, "/manage/"+slug)
		if strings.Contains(body, `href="/edit/`+slug+`"`) || !strings.Contains(body, note) {
			t.Errorf("%s: manage offers Edit or gives no reason", slug)
		}
		status, body := a.get(t, "/edit/"+slug)
		if status != 409 || !strings.Contains(body, note) {
			t.Errorf("%s: edit route %d", slug, status)
		}
	}
	// Pages written in Cairn and Markdown sent through the API keep their
	// Markdown, so both can be edited.
	for _, slug := range []string{"mine", "md"} {
		_, body := a.get(t, "/manage/"+slug)
		if !strings.Contains(body, `href="/edit/`+slug+`"`) || strings.Contains(body, "edit-note") {
			t.Errorf("manage hides Edit for %s", slug)
		}
		if status, body := a.get(t, "/edit/"+slug); status != 200 || !strings.Contains(body, "Body") && slug == "md" {
			t.Errorf("%s: edit route %d", slug, status)
		}
	}
	_, body := a.get(t, "/manage/mine")
	_, body = a.get(t, "/")
	if !strings.Contains(body, `data-slug="mine" data-title="Mine"`) || !regexp.MustCompile(`data-slug="mine"[^>]*data-editable`).MatchString(body) || regexp.MustCompile(`data-slug="html"[^>]*data-editable`).MatchString(body) {
		t.Error("rows do not say which pages can be edited")
	}
	if status, _ := a.get(t, "/edit/missing"); status != 404 {
		t.Error("missing page")
	}
}

func TestEmptyAndFirstRunStates(t *testing.T) {
	a := fxStart(t, "")
	_, body := a.get(t, "/")
	for _, want := range []string{`href="/settings#agents"`, "Connect an agent", `href="/new"><span class="way-icon">`, "Import a file"} {
		if !strings.Contains(body, want) {
			t.Errorf("first run lacks %q", want)
		}
	}
	if strings.Contains(body, "Return to your library") || strings.Contains(body, `id="search"`) {
		t.Error("first run still shows the old dead end or an empty search")
	}
	_, body = a.get(t, "/settings")
	if !strings.Contains(body, `id="agents"`) {
		t.Error("settings has no #agents anchor")
	}
	a.publish(t, "POST", "/api/pages", fxPage("p", "Page", "R", "Body."))
	_, body = a.get(t, "/?q=zzzz")
	if !strings.Contains(body, "No pages match “zzzz”") || !strings.Contains(body, `href="/" data-clear-search>Clear search</a>`) {
		t.Error("no-results state")
	}
	_, body = a.get(t, "/?archived=1")
	if !strings.Contains(body, "Nothing in the archive") || strings.Contains(body, "Every good answer") {
		t.Error("archive empty state")
	}
	_, body = a.get(t, "/?shared=1")
	if !strings.Contains(body, "No shared pages") || strings.Contains(body, "Every good answer") {
		t.Error("shared empty state")
	}
	_, body = a.get(t, "/")
	if !strings.Contains(body, "1 page · Newest first") || strings.Contains(body, "1 pages") {
		t.Error("count is not plural-aware")
	}
}

func TestRelativeDates(t *testing.T) {
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	for created, want := range map[string]string{
		"2026-11-12T14:59:40Z": "Just now",
		"2026-11-12T14:59:00Z": "1 minute ago",
		"2026-11-12T13:00:00Z": "2 hours ago",
		"2026-11-11T23:00:00Z": "Yesterday",
		"2026-11-09T10:00:00Z": "3 days ago",
		"2026-11-01T10:00:00Z": "1 Nov",
		"2025-12-25T10:00:00Z": "25 Dec 2025",
	} {
		if got, full := relativeDate(created, now); got != want || full == "" {
			t.Errorf("%s: %q, want %q", created, got, want)
		}
	}
}

func TestAvatarsAreServedOnceNotPerRow(t *testing.T) {
	a := fxStart(t, "")
	a.s.db.Exec(`INSERT INTO author_profiles(name,photo) VALUES('Codex',?)`, fixtureImage())
	for i := 0; i < 3; i++ {
		a.publish(t, "POST", "/api/pages", fxPage(fmt.Sprintf("p%d", i), "Page", "R", "Body."))
	}
	_, body := a.get(t, "/")
	if strings.Contains(body, "data:image/png") || strings.Count(body, `src="/_library/avatar?name=Codex&amp;v=`) != 3 {
		t.Error("rows embed photos")
	}
	status, header, _ := a.do(t, "GET", "/_library/avatar?name=Codex", nil, nil)
	if status != 200 || header.Get("Content-Type") != "image/png" || !strings.Contains(header.Get("Cache-Control"), "immutable") {
		t.Errorf("avatar: %d %v", status, header)
	}
}

func openRaw(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "cairn.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}
