package cairn_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// shownMarkup is a page without its inline script, so checks read only what the page shows.
func shownMarkup(body []byte) string {
	return regexp.MustCompile(`(?s)<script>.*?</script>`).ReplaceAllString(string(body), "")
}

func TestPublicReaderShowsNothingPrivate(t *testing.T) {
	private, public := startShared(t)
	for _, p := range []map[string]any{
		{"title": "Older neighbour", "agent": "ExampleAgent", "original_request": "PRIVATE-OLDER", "markdown": "Older page.", "slug": "older"},
		{"title": "Shared bulbs", "agent": "ExampleAgent", "original_request": "PRIVATE-ORIGINAL", "request_summary": "PRIVATE-SUMMARY", "request_source": "Hermes", "markdown": "# Shared bulbs\n\n## One\n\nA.\n\n## Two\n\nB.\n\n## Three\n\nC.", "slug": "bulbs"},
		{"title": "Newer neighbour", "agent": "ExampleAgent", "original_request": "PRIVATE-NEWER", "markdown": "Newer page.", "slug": "newer"},
	} {
		if status, _, body := request(t, private, "POST", "/api/pages", p, token); status != 201 {
			t.Fatalf("publish %s: %d %s", p["slug"], status, body)
		}
	}
	if status := ownerPost(t, private, "bulbs", url.Values{"action": {"share"}, "public_slug": {"lamp-guide"}}); status != 303 {
		t.Fatal("share", status)
	}
	// The owner sees the request, its source, the neighbours and the actions.
	_, ownerHeaders, owner := request(t, private, "GET", "/bulbs", nil, "")
	for _, want := range []string{"PRIVATE-SUMMARY", "via Hermes", "Newer neighbour", "Older neighbour", `href="/manage/bulbs"`, `action="/_library/archive"`, `data-link="https://reports.example.com/lamp-guide"`, "Shared. Anyone with the link can read this page."} {
		if !strings.Contains(string(owner), want) {
			t.Errorf("owner reader lacks %q", want)
		}
	}
	if !strings.Contains(ownerHeaders.Get("Content-Security-Policy"), "form-action 'self'") || ownerHeaders.Get("Referrer-Policy") != "same-origin" {
		t.Errorf("owner reader cannot post its own forms: %q %q", ownerHeaders.Get("Content-Security-Policy"), ownerHeaders.Get("Referrer-Policy"))
	}

	status, headers, body := request(t, public, "GET", "/lamp-guide", nil, "")
	if status != 200 {
		t.Fatalf("public reader: %d %s", status, body)
	}
	visitor := shownMarkup(body)
	for _, leak := range []string{
		"PRIVATE-", "via Hermes", "Your request", "only you", "Newer neighbour", "Older neighbour", "Previous page", "Next page",
		`href="/"`, `href="/?`, "/manage/", "/edit/", "/settings", "/_library/", "/new", "/import", `name="csrf"`, "Archive", "Restore",
		"access-status", "Password protected", "Send to Kindle", "reader-menu", "Manage", "HTML via", "reader-format",
	} {
		if strings.Contains(visitor, leak) {
			t.Errorf("public reader contains %q", leak)
		}
	}
	for _, want := range []string{"Prepared by ExampleAgent", "Copy link", "Shared with Cairn", `data-link="https://reports.example.com/lamp-guide"`, `class="reading-shell has-contents rail-layout is-public is-shared"`, `id="toc-list"`} {
		if !strings.Contains(visitor, want) {
			t.Errorf("public reader lacks %q", want)
		}
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "form-action 'none'") {
		t.Errorf("public reader allows form posts: %s", headers.Get("Content-Security-Policy"))
	}
	// Every link a visitor can follow stays on the shared page.
	for _, m := range regexp.MustCompile(`href="([^"]*)"`).FindAllStringSubmatch(visitor, -1) {
		href := m[1]
		if href != "#main" && !strings.HasPrefix(href, "/_content/lamp-guide#") && !strings.HasPrefix(href, "/_ui/") && href != "/lamp-guide?download=1" {
			t.Errorf("public reader links to %q", href)
		}
	}
}

func TestReaderBylineReadsAsASentence(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Bulbs")
	in["request_source"] = "Claude Code"
	if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	_, _, body := request(t, srv, "GET", "/report", nil, "")
	reader := shownMarkup(body)
	if !strings.Contains(reader, `<p class="byline-credit"><span class="byline-who">Prepared by ExampleAgent</span><span class="byline-sep" aria-hidden="true">·</span><time datetime=`) {
		t.Errorf("byline is not \"Prepared by ExampleAgent · date\":\n%s", reader)
	}
	// The stored format is not the reader's business, and the portrait is decoration beside the name.
	for _, gone := range []string{"HTML via", ">HTML<", "reader-format", "Source format", `role="img"`, "ExampleAgent avatar"} {
		if strings.Contains(reader, gone) {
			t.Errorf("byline still has %q", gone)
		}
	}
	if !strings.Contains(reader, `<span class="author-portrait" aria-hidden="true">`) {
		t.Error("portrait is not hidden from screen readers")
	}
	// The request names where it came from, once, beside the request.
	if strings.Count(reader, "via Claude Code") != 2 || !strings.Contains(reader, `<h2 class="request-label" id="request-label"><span class="request-name">Your request</span><span class="request-source">`) {
		t.Errorf("request source is not beside the request")
	}
}

func TestReaderOffersEditOnlyWithMarkdownSource(t *testing.T) {
	private, public := startShared(t)
	if status, _, body := request(t, private, "POST", "/api/pages", page("Sent as HTML"), token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	sent := map[string]any{"title": "Sent as Markdown", "agent": "ExampleAgent", "original_request": "PRIVATE", "markdown": "# Sent\n\nA copy is kept.", "slug": "sent-md"}
	if status, _, body := request(t, private, "POST", "/api/pages", sent, token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	code, h, _ := authorForm(t, private, "/new", url.Values{"title": {"Written here"}, "markdown": {"Some words."}}, private.URL)
	if code != 303 {
		t.Fatal("write", code)
	}
	written := strings.TrimPrefix(strings.SplitN(h.Get("Location"), "?", 2)[0], "/")
	if code, out := importFile(t, private, "notes.md", []byte("# Notes\n\nImported."), "imported-md"); code != 303 {
		t.Fatal("import md", code, out)
	}
	if code, out := importFile(t, private, "page.html", []byte("<h1>Imported HTML</h1>"), "imported-html"); code != 303 {
		t.Fatal("import html", code, out)
	}
	for slug, editable := range map[string]bool{"report": false, "sent-md": true, written: true, "imported-md": true, "imported-html": false} {
		_, _, body := request(t, private, "GET", "/"+slug, nil, "")
		if got := strings.Contains(string(body), `href="/edit/`+slug+`"`); got != editable {
			t.Errorf("%s: edit link %v, want %v", slug, got, editable)
		}
	}
	if status := ownerPost(t, private, written, url.Values{"action": {"share"}}); status != 303 {
		t.Fatal("share", status)
	}
	_, _, body := request(t, public, "GET", "/"+written, nil, "")
	if !strings.Contains(string(body), "Prepared by") || strings.Contains(string(body), "/edit/") {
		t.Error("public reader offers to edit")
	}
}

func TestReaderNeighboursFollowLibraryOrder(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	// Published oldest first, so the library lists d, c, b, a.
	for _, slug := range []string{"a", "b", "c", "d"} {
		in := page("Page " + strings.ToUpper(slug))
		in["slug"] = slug
		if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
			t.Fatalf("%d %s", status, body)
		}
	}
	neighbours := func(slug string) (string, string) {
		t.Helper()
		_, _, body := request(t, srv, "GET", "/"+slug, nil, "")
		find := func(rel string) string {
			m := regexp.MustCompile(`<a class="end-page[^"]*" href="/([^"]+)" rel="` + rel + `"`).FindSubmatch(body)
			if m == nil {
				return ""
			}
			return string(m[1])
		}
		return find("prev"), find("next")
	}
	check := func(slug, previous, next string) {
		t.Helper()
		if p, n := neighbours(slug); p != previous || n != next {
			t.Errorf("%s: previous %q next %q, want %q and %q", slug, p, n, previous, next)
		}
	}
	check("d", "", "c")
	check("c", "d", "b")
	check("a", "b", "")
	// Archived pages leave the library's order and keep their own.
	if status := ownerPost(t, srv, "b", url.Values{"action": {"archive"}}); status != 303 {
		t.Fatal("archive", status)
	}
	check("c", "d", "a")
	check("a", "c", "")
	check("b", "", "")
	if status := ownerPost(t, srv, "d", url.Values{"action": {"archive"}}); status != 303 {
		t.Fatal("archive", status)
	}
	check("b", "d", "")
	check("d", "", "b")
	check("c", "", "a")
	_, _, body := request(t, srv, "GET", "/b", nil, "")
	for _, want := range []string{"Restore page", `name="archived" value="0"`, `href="/?archived=1"`, "Back to archive", `aria-label="More in your archive"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("archived page lacks %q", want)
		}
	}
}

func TestReaderEndOfPageActions(t *testing.T) {
	srv, _ := startShared(t)
	if status, _, body := request(t, srv, "POST", "/api/pages", page("Bulbs"), token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	_, _, body := request(t, srv, "GET", "/report", nil, "")
	reader := string(body)
	end := reader[strings.Index(reader, `<footer class="reader-end">`):]
	for _, want := range []string{`action="/_library/archive"`, `name="slug" value="report"`, `name="archived" value="1"`, `name="return" value="/"`, "Archive page", `href="/manage/report#sharing"`, "Share page", `href="/"`, "Back to library"} {
		if !strings.Contains(end, want) {
			t.Errorf("end of page lacks %q", want)
		}
	}
	if strings.Contains(end, "end-pages") {
		t.Error("a library of one page offers neighbours")
	}
	// The archive form is a real owner form: it posts, archives and returns to the library.
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(end)[1]
	req, _ := http.NewRequest("POST", srv.URL+"/_library/archive", strings.NewReader(url.Values{"csrf": {csrf}, "slug": {"report"}, "archived": {"1"}, "return": {"/"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 303 || !strings.HasPrefix(res.Header.Get("Location"), "/?") {
		t.Fatalf("archive from the reader: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	_, _, body = request(t, srv, "GET", "/report", nil, "")
	if !strings.Contains(string(body), "Restore page") {
		t.Error("page was not archived")
	}
	// Shared pages copy their link at the end instead of asking to share.
	if status := ownerPost(t, srv, "report", url.Values{"action": {"restore"}}); status != 303 {
		t.Fatal("restore", status)
	}
	if status := ownerPost(t, srv, "report", url.Values{"action": {"share"}}); status != 303 {
		t.Fatal("share", status)
	}
	_, _, body = request(t, srv, "GET", "/report", nil, "")
	end = string(body)[strings.Index(string(body), `<footer class="reader-end">`):]
	if strings.Contains(end, "Share page") || !strings.Contains(end, `class="button quiet copy-link" data-copy-link hidden`) || !strings.Contains(end, "Archive page") {
		t.Error("a shared page should offer its link at the end")
	}
}

func TestReaderCoverFollowsTheReport(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	long := "Comparison of every 27-inch 4K monitor under £400 that supports USB-C power delivery at 65 watts or more"
	for _, p := range []map[string]any{
		{"title": "Custom page", "agent": "A", "original_request": "R", "html": "<!doctype html><title>x</title><h1>Custom page, its own way</h1><h2 id=a>A</h2><h2 id=b>B</h2><h2 id=c>C</h2>", "slug": "custom"},
		{"title": "Plain fragment", "agent": "A", "original_request": "R", "html": "<p>No heading at all.</p>", "slug": "plain"},
		{"title": "Echoed title", "agent": "A", "original_request": "R", "markdown": "# Echoed title\n\n## One\n\n## Two\n\n## Three", "slug": "echo"},
		{"title": long, "agent": "A", "original_request": "R", "markdown": "Short.", "slug": "long"},
	} {
		if status, _, body := request(t, srv, "POST", "/api/pages", p, token); status != 201 {
			t.Fatalf("%s: %d %s", p["slug"], status, body)
		}
	}
	get := func(slug string) string {
		_, _, body := request(t, srv, "GET", "/"+slug, nil, "")
		return string(body)
	}
	// Custom HTML carries its own title and its own design: a slim neutral strip keeps the title for
	// screen readers only, and its contents never float as a rail over the page.
	custom := get("custom")
	if !strings.Contains(custom, `<header class="reader-cover reader-strip owner-strip has-own-title"`) || !strings.Contains(custom, `<h1 class="report-title sr-only">Custom page</h1>`) || strings.Contains(custom, "rail-layout") || strings.Contains(custom, "blue-hour") || !strings.Contains(custom, "has-contents") {
		t.Errorf("custom HTML cover:\n%s", custom)
	}
	if plain := get("plain"); !strings.Contains(plain, `<h1 class="report-title">Plain fragment</h1>`) || strings.Contains(plain, "has-own-title") {
		t.Error("a report without a title lost the cover title")
	}
	echo := get("echo")
	if !strings.Contains(echo, `<h1 class="report-title">Echoed title</h1>`) || !strings.Contains(echo, `class="reading-shell has-contents rail-layout is-owner"`) {
		t.Error("a Markdown page that repeats its title should show the cover title and the rail layout")
	}
	// Long titles step down a size and keep compound words whole.
	if l := get("long"); !strings.Contains(l, `<h1 class="report-title is-long">Comparison of every <span class="compound">27-inch</span> 4K monitor under £400 that supports <span class="compound">USB-C</span> power delivery`) {
		t.Errorf("long title:\n%s", l)
	}
	// Without JavaScript the contents still open from the cover.
	if !strings.Contains(echo, `<noscript><details class="contents-fallback"><summary>Contents</summary><ol><li><a href="/_content/echo#one" target="report" data-id="one">One</a></li>`) {
		t.Error("no contents without JavaScript")
	}
}

func TestReaderNeverShowsAnEmptyRequest(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	code, h, _ := authorForm(t, srv, "/new", url.Values{"title": {"Owner note"}, "markdown": {"Just words."}}, srv.URL)
	if code != 303 {
		t.Fatal(code)
	}
	_, _, body := request(t, srv, "GET", h.Get("Location"), nil, "")
	if strings.Contains(shownMarkup(body), "report-request") || strings.Contains(shownMarkup(body), "Your request") {
		t.Error("a page without a request shows a request block")
	}
}
