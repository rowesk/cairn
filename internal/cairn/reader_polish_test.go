package cairn_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestReaderSaysASharedPageNeedsItsPassword(t *testing.T) {
	private, public := startShared(t)
	for _, slug := range []string{"private", "shared", "locked"} {
		in := map[string]any{"title": "Page " + slug, "agent": "ExampleAgent", "original_request": "R", "markdown": "# Page " + slug + "\n\nWords.", "slug": slug}
		if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
			t.Fatalf("%s: %d %s", slug, status, body)
		}
	}
	for _, step := range []struct {
		slug string
		form url.Values
	}{
		{"shared", url.Values{"action": {"share"}}},
		{"locked", url.Values{"action": {"share"}}},
		{"locked", url.Values{"action": {"password"}, "password": {"warm-house-2026"}}},
	} {
		if code, _, body := authorForm(t, private, "/manage/"+step.slug, step.form, private.URL); code != 303 {
			t.Fatalf("%s %v: %d %s", step.slug, step.form, code, body)
		}
	}
	get := func(slug string) string {
		t.Helper()
		_, _, body := request(t, private, "GET", "/"+slug, nil, "")
		return shownMarkup(body)
	}
	lock := `<rect x="5" y="10" width="14" height="11" rx="2"/>`
	locked := get("locked")
	status := regexp.MustCompile(`(?s)<p class="access-status">(.*?)</p>`).FindStringSubmatch(locked)
	if status == nil || !strings.Contains(status[1], "Shared with a password") || !strings.Contains(status[1], lock) {
		t.Errorf("a password-protected page's status: %v", status)
	}
	sentence := regexp.MustCompile(`(?s)<p class="menu-status">(.*?)</p>`).FindStringSubmatch(locked)
	if sentence == nil || !strings.Contains(sentence[1], "Shared with a password. Visitors need the link and the password.") || !strings.Contains(sentence[1], lock) {
		t.Errorf("a password-protected page's menu sentence: %v", sentence)
	}
	// Nowhere on a protected page may the reader say anyone with the link can read it.
	for _, wrong := range []string{"Anyone with the link", "Password protected"} {
		if strings.Contains(locked, wrong) {
			t.Errorf("protected page says %q", wrong)
		}
	}
	if !strings.Contains(locked, `class="byline-access"`) || !strings.Contains(locked, " is-protected") {
		t.Error("the byline does not carry the status for phones, or the body lacks is-protected")
	}
	for slug, want := range map[string]string{"shared": "Shared. Anyone with the link can read this page.", "private": "Private. Only you can open this page."} {
		if page := get(slug); !strings.Contains(page, want) || strings.Contains(page, "Shared with a password") {
			t.Errorf("%s page does not say %q", slug, want)
		}
	}
	// Visitors never see the status at all.
	_, _, body := request(t, public, "GET", "/shared", nil, "")
	if visitor := shownMarkup(body); strings.Contains(visitor, "access-status") || strings.Contains(visitor, "byline-access") {
		t.Error("the visitor view states the page's status")
	}
}

func TestReaderOffersTheOriginalOnlyForImportedFiles(t *testing.T) {
	private, public := startShared(t)
	if status, _, body := request(t, private, "POST", "/api/pages", page("Sent as HTML"), token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	sent := map[string]any{"title": "Sent as Markdown", "agent": "ExampleAgent", "original_request": "R", "markdown": "# Sent\n\nNine words an agent sent through the API today.", "slug": "sent-md"}
	if status, _, body := request(t, private, "POST", "/api/pages", sent, token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	code, h, _ := authorForm(t, private, "/new", url.Values{"title": {"Written here"}, "markdown": {"Some words."}}, private.URL)
	if code != 303 {
		t.Fatal("write", code)
	}
	written := strings.TrimPrefix(strings.SplitN(h.Get("Location"), "?", 2)[0], "/")
	for name, slug := range map[string]string{"notes.md": "imported-md", "page.html": "imported-html", "notes.txt": "imported-txt"} {
		if code, out := importFile(t, private, name, []byte("# Imported\n\nWords."), slug); code != 303 {
			t.Fatal("import", name, code, out)
		}
	}
	for slug, offered := range map[string]bool{"report": false, "sent-md": false, written: false, "imported-md": false, "imported-html": true, "imported-txt": true} {
		_, _, body := request(t, private, "GET", "/"+slug, nil, "")
		if got := strings.Contains(string(body), "Download original"); got != offered {
			t.Errorf("%s: Download original %v, want %v", slug, got, offered)
		}
	}
	if code, _, _ := authorForm(t, private, "/manage/imported-html", url.Values{"action": {"share"}}, private.URL); code != 303 {
		t.Fatal("share", code)
	}
	if _, _, body := request(t, public, "GET", "/imported-html", nil, ""); strings.Contains(string(body), "Download original") || strings.Contains(string(body), "download=1") {
		t.Error("the visitor view offers the original")
	}
}

func TestReaderChromeIsOneGridForEveryPageType(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	for _, p := range []map[string]any{
		{"title": "A report", "agent": "A", "original_request": "R", "markdown": "# A report\n\n## One\n\nA.\n\n## Two\n\nB.\n\n## Three\n\nC.", "slug": "report-page"},
		{"title": "A short page", "agent": "A", "original_request": "R", "markdown": "Just a line.", "slug": "short-page"},
		{"title": "A comparison", "agent": "A", "original_request": "R", "template": "comparison", "markdown": "# A comparison\n\n## Table\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n## Verdict\n\nA.\n\n## Why\n\nB.", "slug": "comparison-page"},
		{"title": "A custom page", "agent": "A", "original_request": "R", "html": "<!doctype html><title>x</title><h1>Its own title</h1><h2 id=a>A</h2><h2 id=b>B</h2><h2 id=c>C</h2>", "slug": "custom-page"},
	} {
		if status, _, body := request(t, srv, "POST", "/api/pages", p, token); status != 201 {
			t.Fatalf("%s: %d %s", p["slug"], status, body)
		}
	}
	nav := regexp.MustCompile(`(?s)<div class="reading-nav reader-grid">\s*<a class="brand" href="/">.*?</a>\s*<div class="reading-actions">\s*<p class="access-status">.*?</p>\s*<button type="button" class="nav-button copy-link wide-action" data-copy-link hidden>.*?</button>\s*<button type="button" class="nav-button more-button" popovertarget="reader-menu"`)
	for slug, want := range map[string]struct{ cover, body string }{
		"report-page":     {`<header class="reader-cover blue-hour"`, `class="reading-shell has-contents rail-layout is-owner"`},
		"short-page":      {`<header class="reader-cover blue-hour"`, `class="reading-shell is-owner"`},
		"comparison-page": {`<header class="reader-cover blue-hour"`, `class="reading-shell has-contents is-owner"`},
		"custom-page":     {`<header class="reader-cover reader-strip owner-strip has-own-title"`, `class="reading-shell has-contents is-owner is-custom report-fixed"`},
	} {
		_, _, body := request(t, srv, "GET", "/"+slug, nil, "")
		reader := shownMarkup(body)
		// The same toolbar on every page type: brand, status, Copy link, More. Nothing else is a button there.
		if !nav.MatchString(reader) {
			t.Errorf("%s: toolbar is not brand, status, Copy link and More", slug)
		}
		if !strings.Contains(reader, want.cover) || !strings.Contains(reader, want.body) {
			t.Errorf("%s: want %s and %s", slug, want.cover, want.body)
		}
		// Edit, Kindle and Manage live in the More menu, never as cover buttons.
		cover := reader[strings.Index(reader, "<header"):strings.Index(reader, "</header>")]
		for _, gone := range []string{"/edit/", "/manage/", "kindle"} {
			if strings.Contains(cover, gone) {
				t.Errorf("%s: cover still holds %s", slug, gone)
			}
		}
	}
	// Custom pages never float a rail: their contents open from the bar and the More menu.
	_, _, body := request(t, srv, "GET", "/custom-page", nil, "")
	custom := shownMarkup(body)
	if strings.Contains(custom, "rail-layout") || !strings.Contains(custom, `class="compact-contents"`) || !strings.Contains(custom, `class="menu-contents"`) || !strings.Contains(custom, `class="menu-details"`) {
		t.Error("custom page contents should open from the bar and the menu, not a rail")
	}
}

func TestReaderHonoursTheShortcutSetting(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Keys")
	delete(in, "html")
	in["markdown"] = "# Keys\n\n## One\n\nA.\n\n## Two\n\nB."
	if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	_, _, body := request(t, srv, "GET", "/report", nil, "")
	if strings.Contains(string(body), `data-shortcuts="off"`) {
		t.Fatal("shortcuts start off")
	}
	if code, _, out := authorForm(t, srv, "/settings", url.Values{"action": {"shortcuts"}, "shortcuts": {"off"}}, srv.URL); code != 303 {
		t.Fatalf("turn shortcuts off: %d %s", code, out)
	}
	_, _, body = request(t, srv, "GET", "/report", nil, "")
	if !strings.Contains(string(body), `<html lang="en" data-theme="system" data-shortcuts="off">`) || strings.Contains(string(body), "aria-keyshortcuts") {
		t.Error("the reader does not carry the off setting, or still announces its keys")
	}
}

func TestReaderNoticesFloatWithoutMovingTheCover(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	if status, _, body := request(t, srv, "POST", "/api/pages", page("Bulbs"), token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	_, _, body := request(t, srv, "GET", "/report?saved=1", nil, "")
	reader := string(body)
	cover := reader[strings.Index(reader, "<header"):strings.Index(reader, "</header>")]
	if strings.Contains(cover, "Saved.") || !strings.Contains(reader, `<div class="toasts" id="toasts">`) || !strings.Contains(reader[strings.Index(reader, `<div class="toasts"`):], "Saved.") {
		t.Error("the saved notice should be a toast, outside the cover")
	}
}

func TestReaderCarriesAPlausibleHeightLimit(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	short, long := page("Short"), page("Long")
	short["slug"], long["slug"] = "short", "long"
	delete(short, "html")
	delete(long, "html")
	short["markdown"] = "One line."
	long["markdown"] = strings.Repeat("A paragraph of reading text that goes on for a while. ", 4000)
	for _, in := range []map[string]any{short, long} {
		if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
			t.Fatalf("%d %s", status, body)
		}
	}
	limit := func(slug string) int {
		_, _, body := request(t, srv, "GET", "/"+slug, nil, "")
		m := regexp.MustCompile(`data-report-limit="(\d+)"`).FindSubmatch(body)
		if m == nil {
			t.Fatalf("%s has no height limit", slug)
		}
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	if s, l := limit("short"), limit("long"); s > 20000 || l <= s || l > 200000 {
		t.Errorf("limits: short %d, long %d", s, l)
	}
}
