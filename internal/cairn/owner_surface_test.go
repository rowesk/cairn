package cairn_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// settingsJSON posts a settings form the way its own script does.
func settingsJSON(t *testing.T, srv string, values url.Values, csrf string) (int, map[string]any) {
	t.Helper()
	values.Set("csrf", csrf)
	req, _ := http.NewRequest("POST", srv+"/settings", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv)
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var reply map[string]any
	_ = json.NewDecoder(res.Body).Decode(&reply)
	return res.StatusCode, reply
}

// Every owner form checks itself: novalidate, so the browser shows no
// bubbles, with the messages beside the fields.
func TestOwnerFormsShowNoBrowserBubbles(t *testing.T) {
	private, public := startShared(t)
	in := page("Bulb comparison")
	request(t, private, "POST", "/api/pages", in, token)
	if code := ownerPost(t, private, "report", url.Values{"action": {"share"}}); code != 303 {
		t.Fatal(code)
	}
	if code := ownerPost(t, private, "report", url.Values{"action": {"password"}, "password": {"long enough"}}); code != 303 {
		t.Fatal(code)
	}
	forms := regexp.MustCompile(`<form\b[^>]*>`)
	for _, path := range []string{"/new", "/import", "/manage/report", "/settings"} {
		_, _, body := request(t, private, "GET", path, nil, "")
		for _, tag := range forms.FindAllString(string(body), -1) {
			// Forms with nothing to type (buttons, the switch, Undo) need no checks.
			if strings.Contains(tag, "novalidate") || strings.Contains(tag, `id="keyboard-form"`) || strings.Contains(tag, `id="theme-form"`) || !strings.Contains(tag, "class=") && !strings.Contains(tag, "id=") {
				continue
			}
			if strings.Contains(tag, "title-form") || strings.Contains(tag, "sharing-form") || strings.Contains(tag, "stacked-form") || strings.Contains(tag, "settings-fields") || strings.Contains(tag, "editor-form") || strings.Contains(tag, "connect-agent") {
				t.Errorf("%s: %s can show browser bubbles", path, tag)
			}
		}
	}
	_, _, gate := request(t, public, "GET", "/report", nil, "")
	if !strings.Contains(string(gate), `<form method="post" class="gate-form" novalidate>`) || !strings.Contains(string(gate), `data-missing="Enter the password to read this page."`) {
		t.Error("the password gate can show a browser bubble")
	}
	_, _, manage := request(t, private, "GET", "/manage/report", nil, "")
	for _, want := range []string{`<form method="post" class="title-form" novalidate>`, `class="sharing-form" id="sharing-form" novalidate`, `data-too-short="Use at least 8 characters."`, `id="share-password-error"`, `id="page-title-error"`, `id="copy-both"`, "<strong>Shared with a password.</strong>"} {
		if !strings.Contains(string(manage), want) {
			t.Errorf("manage lacks %q", want)
		}
	}
	// One address field: the host fixed, the name editable, and Copy beside it. The address is not shown twice.
	if strings.Contains(string(manage), `id="public-address"`) || strings.Contains(string(manage), "readonly spellcheck") || !strings.Contains(string(manage), `<span aria-hidden="true">reports.example.com/</span>`) || !strings.Contains(string(manage), `name="public_slug" value="report"`) || !strings.Contains(string(manage), `data-copy-value="https://reports.example.com/report"`) {
		t.Error("manage shows the public address twice")
	}
}

// A blank title takes the file's first heading; a PDF shows as a file card
// with its name, kind and size, and a sensible excerpt.
func TestImportTitlesAndFileCards(t *testing.T) {
	private, _ := startShared(t)
	if code, body := importFile(t, private, "dentist.md", []byte("# Dentist, 4 November\n\nTwo small fillings."), "dentist"); code != 303 {
		t.Fatal(code, body)
	}
	_, _, saved := request(t, private, "GET", "/api/pages/dentist", nil, token)
	if !strings.Contains(string(saved), `"title":"Dentist, 4 November"`) {
		t.Fatalf("title not taken from the heading: %s", saved)
	}
	pdf := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 245760)...)
	if code, body := importFile(t, private, "boiler-service.pdf", pdf, "boiler"); code != 303 {
		t.Fatal(code, body)
	}
	_, _, content := request(t, private, "GET", "/_content/boiler", nil, "")
	for _, want := range []string{`<div class="file-card">`, `<strong>boiler-service.pdf</strong>`, "PDF · 240\u00a0KB", `<a class="file-card-download" href="?download=1" rel="noopener" target="_blank">Download</a>`} {
		if !strings.Contains(string(content), want) {
			t.Errorf("file card lacks %q", want)
		}
	}
	if strings.Contains(string(content), "full-page view") {
		t.Error("the card still tells the owner to look elsewhere")
	}
	_, _, list := request(t, private, "GET", "/", nil, "")
	// The row reads "PDF · 240 KB": the title already names the file (p9).
	if !strings.Contains(string(list), "<p class=\"excerpt\">PDF · 240\u00a0KB</p>") {
		t.Errorf("PDF excerpt: %s", regexp.MustCompile(`<p class="excerpt">[^<]*</p>`).FindAllString(string(list), -1))
	}
	_, _, form := request(t, private, "GET", "/import", nil, "")
	if !strings.Contains(string(form), "a PDF or a Word document, up to 10 MB") || !strings.Contains(string(form), `class="dropzone is-compact`) || !strings.Contains(string(form), `<form method="post" enctype="multipart/form-data" class="stacked-form" novalidate>`) {
		t.Error("import form: hint, supporting images zone or novalidate")
	}
}

// Keyboard shortcuts are a setting, kept with the other preferences and
// shown to every owner page as data-shortcuts="off".
func TestShortcutSetting(t *testing.T) {
	private, _ := startShared(t)
	request(t, private, "POST", "/api/pages", page("Bulb comparison"), token)
	csrf := formToken(t, private)
	for _, path := range []string{"/", "/new", "/settings"} {
		_, _, body := request(t, private, "GET", path, nil, "")
		if strings.Contains(string(body), `data-shortcuts="off"`) {
			t.Fatalf("%s: shortcuts start off", path)
		}
	}
	code, h, _ := postForm(t, private, "/settings", url.Values{"csrf": {csrf}, "action": {"shortcuts"}}, private.URL)
	if code != 303 || h.Get("Location") != "/settings?saved=keyboard-off#keyboard" {
		t.Fatalf("turn off: %d %s", code, h.Get("Location"))
	}
	for _, path := range []string{"/", "/new", "/import", "/manage/report", "/settings", "/nope"} {
		_, _, body := request(t, private, "GET", path, nil, "")
		if !strings.Contains(string(body), `<html lang="en" data-theme="system" data-shortcuts="off">`) {
			t.Errorf("%s does not know shortcuts are off", path)
		}
	}
	_, _, library := request(t, private, "GET", "/", nil, "")
	if strings.Contains(string(library), `id="show-shortcuts"`) {
		t.Error("the library still offers its shortcut list")
	}
	_, _, settings := request(t, private, "GET", "/settings?saved=keyboard-off", nil, "")
	if !strings.Contains(string(settings), `id="shortcuts" name="shortcuts" value="on" aria-describedby="shortcuts-help">`) || !strings.Contains(string(settings), "Keyboard shortcuts are off.") {
		t.Error("settings does not show the switch off with its confirmation")
	}
	status, reply := settingsJSON(t, private.URL, url.Values{"action": {"shortcuts"}, "shortcuts": {"on"}}, csrf)
	if status != 200 || reply["ok"] != true || reply["message"] != "Keyboard shortcuts are on." {
		t.Fatalf("turn on by fetch: %d %v", status, reply)
	}
	_, _, library = request(t, private, "GET", "/", nil, "")
	if strings.Contains(string(library), `data-shortcuts="off"`) {
		t.Error("shortcuts stayed off")
	}
}

// Settings fields save themselves through the page's fetch, with each
// field's problem returned by name; without JavaScript the forms post.
func TestSettingsSaveAsYouGo(t *testing.T) {
	private, _ := startShared(t)
	csrf := formToken(t, private)
	status, reply := settingsJSON(t, private.URL, url.Values{"action": {"defaults"}, "author": {"Chris"}, "template": {"report"}, "link_length": {"4"}}, csrf)
	if status != 200 || reply["ok"] != true || reply["message"] != "Defaults saved. Your next page uses them." {
		t.Fatalf("defaults: %d %v", status, reply)
	}
	status, reply = settingsJSON(t, private.URL, url.Values{"action": {"defaults"}, "author": {"Chris"}, "template": {"report"}, "link_length": {"9"}}, csrf)
	errs, _ := reply["errors"].(map[string]any)
	if status != 400 || reply["ok"] != false || errs["link_length"] != "Choose a length from 3 to 7 characters." {
		t.Fatalf("bad length: %d %v", status, reply)
	}
	status, reply = settingsJSON(t, private.URL, url.Values{"action": {"defaults"}, "author": {"Chris"}, "template": {"report"}, "link_length": {"4"}}, "stale")
	if status != 403 || reply["ok"] != false {
		t.Fatalf("stale token: %d %v", status, reply)
	}
	// A Kindle address that isn't Amazon's is kept, with a warning.
	status, reply = settingsJSON(t, private.URL, url.Values{"action": {"kindle"}, "kindle_address": {"chris@gmail.com"}}, csrf)
	if status != 200 || reply["ok"] != true || !strings.Contains(reply["warning"].(string), "@kindle.com or @free.kindle.com") {
		t.Fatalf("kindle warning: %d %v", status, reply)
	}
	code, h, _ := postForm(t, private, "/settings", url.Values{"csrf": {csrf}, "action": {"kindle"}, "kindle_address": {"chris@gmail.com"}}, private.URL)
	if code != 303 || h.Get("Location") != "/settings?saved=kindle-unusual#kindle" {
		t.Fatalf("kindle without script: %d %s", code, h.Get("Location"))
	}
	_, _, body := request(t, private, "GET", "/settings?saved=kindle-unusual", nil, "")
	// One warning, under the field; the confirmation only confirms (p9 changed
	// this from a confirmation that repeated the warning).
	if !strings.Contains(string(body), `id="kindle-address-warning">`) || strings.Count(string(body), "Amazon’s Kindle addresses end") != 1 || !strings.Contains(string(body), `<p class="toast-text">Kindle address saved.</p>`) {
		t.Error("the warning does not show once after a plain save")
	}
	// The default author's own row offers no key and says why.
	row := regexp.MustCompile(`(?s)<li class="person-row">.*?</li>`).FindAllString(string(body), -1)
	found := false
	for _, r := range row {
		if strings.Contains(r, `<strong class="person-name">Chris</strong>`) {
			found = true
			if strings.Contains(r, "manage-key\"") || !strings.Contains(r, "keys are for agents") {
				t.Errorf("owner row: %s", r)
			}
		}
	}
	if !found {
		t.Error("no row for the default author")
	}
	// Key and photo controls need JavaScript, so they ship hidden.
	if !strings.Contains(string(body), `<form class="connect-agent" id="connect-agent" novalidate hidden data-owner="Chris">`) {
		t.Error("connect form shows without JavaScript")
	}
	// Switching theme can update the browser's toolbar colour for every theme.
	if !strings.Contains(string(body), `<meta name="theme-color" data-for="dark" content="#111b22">`) || !strings.Contains(string(body), `data-for="system" content="#111b22" media="(prefers-color-scheme: dark)"`) {
		t.Error("theme colours missing for the switch")
	}
}

// The top bar is the same on every owner page: brand, the page's place,
// Settings. The library shows only collections that hold something, and
// New page only where it adds a page.
func TestOneTopBarAndAQuietLibrary(t *testing.T) {
	srv, done := start(t, t.TempDir())
	defer done()
	_, _, first := request(t, srv, "GET", "/", nil, "")
	if strings.Contains(string(first), `class="topnav"`) || strings.Contains(string(first), `class="split-create"`) || !strings.Contains(string(first), "Welcome to Cairn") {
		t.Error("first run shows empty collections or a second way to write")
	}
	_, _, archive := request(t, srv, "GET", "/?archived=1", nil, "")
	if strings.Contains(string(archive), "Ways to add your first page") || !strings.Contains(string(archive), "Nothing in the archive") || strings.Contains(string(archive), `class="split-create"`) {
		t.Error("the archive borrows the first-run page or offers New page")
	}
	request(t, srv, "POST", "/api/pages", page("Bulb comparison"), token)
	_, _, library := request(t, srv, "GET", "/", nil, "")
	for _, unwanted := range []string{"Every good answer", `href="/?shared=1"`, `href="/?archived=1"`, ">0<", "Private</span></span>", `class="badge`} {
		if strings.Contains(string(library), unwanted) {
			t.Errorf("library shows %q", unwanted)
		}
	}
	// Private is the default, so rows announce access only when it is not (p9).
	if strings.Contains(string(library), `<span class="sr-only">Private</span>`) || !strings.Contains(string(library), `class="split-create"`) {
		t.Error("library announces the default access or lost New page")
	}
	for _, path := range []string{"/", "/settings", "/new", "/import", "/manage/report"} {
		_, _, body := request(t, srv, "GET", path, nil, "")
		if !strings.Contains(string(body), `<a class="brand" href="/">`) || !strings.Contains(string(body), `<a class="topbar-settings" href="/settings"`) {
			t.Errorf("%s: not the shared top bar", path)
		}
	}
	_, _, settings := request(t, srv, "GET", "/settings", nil, "")
	if !strings.Contains(string(settings), `<a class="topbar-settings" href="/settings" aria-current="page">`) {
		t.Error("settings does not mark itself in the bar")
	}
	_, _, missing := request(t, srv, "GET", "/no-such-page", nil, "")
	if !strings.Contains(string(missing), `<body class="problem-body">`) || strings.Contains(string(missing), "back-link") {
		t.Error("the owner 404 is not the visitor page's sibling")
	}
}
