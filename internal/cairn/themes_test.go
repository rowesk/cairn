package cairn_test

import (
	"database/sql"
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

	"github.com/rowesk/cairn/internal/cairn"
)

// themeOf reads the theme a page renders in.
func themeOf(t *testing.T, server *httptest.Server, path string) (int, string, string) {
	t.Helper()
	code, _, body := request(t, server, "GET", path, nil, "")
	m := regexp.MustCompile(`<html lang="en" data-theme="([a-z]+)">`).FindSubmatch(body)
	if m == nil {
		return code, "", string(body)
	}
	return code, string(m[1]), string(body)
}

// rawDB opens an installation's database directly, as an older or newer
// build would have left it.
func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "cairn.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func openThemed(t *testing.T, dir string) (*httptest.Server, *httptest.Server, func()) {
	t.Helper()
	app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	private := httptest.NewServer(app)
	public := httptest.NewServer(app.PublicHandler())
	return private, public, func() { private.Close(); public.Close(); app.Close() }
}

func TestThemeSelectionsPersistAndStayInTheirViews(t *testing.T) {
	dir := t.TempDir()
	private, public, closeApp := openThemed(t, dir)
	defer func() { closeApp() }()
	assertTheme := func(server *httptest.Server, path, theme string, status int) {
		t.Helper()
		if code, got, _ := themeOf(t, server, path); code != status || got != theme {
			t.Fatalf("%s: wanted status %d and theme %s, got status %d and %q", path, status, theme, code, got)
		}
	}
	// A new installation follows the device.
	assertTheme(private, "/settings", "system", 200)
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Theme report"}, "markdown": {"Original report content"}, "slug": {"theme-report"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal("create report", code)
	}
	_, _, originalContent := request(t, private, "GET", "/_content/theme-report", nil, "")
	_, _, originalSource := request(t, private, "GET", "/theme-report?download=1", nil, "")
	// The older three-field form still works.
	choices := url.Values{"action": {"themes"}, "admin_theme": {"minimal"}, "reader_theme": {"futuristic"}, "public_theme": {"cairn"}}
	if code, _, _ := authorForm(t, private, "/settings", choices, "https://elsewhere.example"); code != 403 {
		t.Fatal("cross-origin theme write accepted", code)
	}
	assertTheme(private, "/settings", "system", 200)
	if code, _, _ := authorForm(t, private, "/settings", choices, private.URL); code != 303 {
		t.Fatal("save themes")
	}
	for _, path := range []string{"/", "/settings", "/new", "/import", "/manage/theme-report", "/manage/theme-report/delete", "/no-such-page"} {
		want := 200
		if path == "/no-such-page" {
			want = 404
		}
		assertTheme(private, path, "minimal", want)
	}
	assertTheme(private, "/theme-report", "futuristic", 200)
	assertTheme(public, "/theme-report", "cairn", 200)
	assertTheme(public, "/no-such-page", "cairn", 404)
	// Themes never change what a page holds.
	_, _, content := request(t, private, "GET", "/_content/theme-report", nil, "")
	_, _, source := request(t, private, "GET", "/theme-report?download=1", nil, "")
	if string(content) != string(originalContent) || string(source) != string(originalSource) {
		t.Fatal("themes changed report content or source")
	}
	for _, bad := range []string{"", "unknown", `"><script>alert(1)</script>`} {
		choices.Set("public_theme", bad)
		choices.Set("admin_theme", "futuristic")
		if code, _, _ := authorForm(t, private, "/settings", choices, private.URL); code != 400 {
			t.Fatal("accepted invalid theme", code)
		}
		assertTheme(private, "/settings", "minimal", 200)
		assertTheme(public, "/theme-report", "cairn", 200)
	}
	// Other settings never reset themes.
	if code, _, _ := authorForm(t, private, "/settings", url.Values{"action": {"defaults"}, "author": {"Owner"}, "template": {"report"}, "link_length": {"3"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	closeApp()
	private, public, closeApp = openThemed(t, dir)
	assertTheme(private, "/settings", "minimal", 200)
	assertTheme(private, "/theme-report", "futuristic", 200)
	assertTheme(public, "/theme-report", "cairn", 200)
	if code, _, _ := authorForm(t, private, "/manage/theme-report", url.Values{"action": {"password"}, "password": {"secret-words"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	assertTheme(public, "/theme-report", "cairn", 401)
	if code, _, _ := request(t, public, "GET", "/settings", nil, ""); code != 404 {
		t.Fatal("settings exposed publicly", code)
	}
}

// One click on a card sets every view that hasn't been given a theme of its
// own; the per-view choices hold until they are set back to the same.
func TestOneThemeEverywhereWithPerViewChoices(t *testing.T) {
	private, public, closeApp := openThemed(t, t.TempDir())
	defer closeApp()
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Shared page"}, "markdown": {"# Shared page\n\nText."}, "slug": {"shared"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	views := func() [3]string {
		_, admin, _ := themeOf(t, private, "/")
		_, reader, _ := themeOf(t, private, "/shared")
		_, visitor, _ := themeOf(t, public, "/shared")
		return [3]string{admin, reader, visitor}
	}
	save := func(fields url.Values) {
		t.Helper()
		fields.Set("action", "themes")
		if code, h, body := authorForm(t, private, "/settings", fields, private.URL); code != 303 || h.Get("Location") != "/settings?saved=appearance#appearance" {
			t.Fatal(code, body)
		}
	}
	save(url.Values{"theme": {"dark"}})
	if got := views(); got != [3]string{"dark", "dark", "dark"} {
		t.Fatal("one theme for every view", got)
	}
	_, _, settings := request(t, private, "GET", "/settings?saved=appearance", nil, "")
	if !strings.Contains(string(settings), "Dark is on everywhere, shared pages included.") {
		t.Fatal("no confirmation in Appearance")
	}
	save(url.Values{"theme": {"dark"}, "reader_theme": {""}, "public_theme": {"minimal"}})
	save(url.Values{"theme": {"futuristic"}, "reader_theme": {""}, "public_theme": {"minimal"}})
	if got := views(); got != [3]string{"futuristic", "futuristic", "minimal"} {
		t.Fatal("shared pages kept their own theme", got)
	}
	_, _, settings = request(t, private, "GET", "/settings?saved=appearance", nil, "")
	page := string(settings)
	if !strings.Contains(page, "Futuristic is on for your library. Shared pages use Minimal.") || !strings.Contains(page, `<details class="theme-views" open>`) || !regexp.MustCompile(`<option value="minimal" selected>Minimal</option>`).MatchString(page) {
		t.Fatal("per-view choice not shown open with its value")
	}
	// Choosing the library's own theme for a view means "the same".
	save(url.Values{"theme": {"cairn"}, "reader_theme": {"cairn"}, "public_theme": {"cairn"}})
	save(url.Values{"theme": {"minimal"}})
	if got := views(); got != [3]string{"minimal", "minimal", "minimal"} {
		t.Fatal("a matching per-view choice should follow the library", got)
	}
}

func TestAppearanceSavesInPlaceAndWithoutJavaScript(t *testing.T) {
	private, _, closeApp := openThemed(t, t.TempDir())
	defer closeApp()
	_, _, body := request(t, private, "GET", "/settings", nil, "")
	page := string(body)
	form := strings.SplitN(strings.SplitN(page, `id="theme-form"`, 2)[1], "</form>", 2)[0]
	// Without JavaScript: five radio buttons, the chosen one checked, the
	// per-view selects and a Save button, in a form that posts.
	for _, want := range []string{`<input type="radio" name="theme" value="system" checked`, `<input type="radio" name="theme" value="dark"`, `<input type="radio" name="theme" value="cairn"`, `<input type="radio" name="theme" value="minimal"`, `<input type="radio" name="theme" value="futuristic"`, `<select id="public-theme" name="public_theme"`, `<option value="" selected>Same as the theme above</option>`, `<button>Save theme</button>`, `<summary>Customise per view</summary>`} {
		if !strings.Contains(form, want) {
			t.Errorf("appearance form lacks %s", want)
		}
	}
	if !strings.Contains(page, `<form class="appearance" method="post" id="theme-form"`) {
		t.Error("appearance form does not post")
	}
	for _, retired := range []string{"Admin view", "Save themes", "Omarchy"} {
		if strings.Contains(page, retired) {
			t.Errorf("settings still says %q", retired)
		}
	}
	// Each card previews its theme from the real tokens: System shows both of its looks.
	if strings.Count(form, `class="sample-face"`) != 6 || !strings.Contains(form, `<span class="sample-face" data-preset="dark">`) {
		t.Error("theme samples missing")
	}
	// The cards' fetch: JSON in, JSON out, nothing else changes.
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page)[1]
	post := func(values url.Values, origin string) (int, map[string]any) {
		values.Set("csrf", csrf)
		values.Set("action", "themes")
		req, _ := http.NewRequest("POST", private.URL+"/settings", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", origin)
		res, err := private.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var reply map[string]any
		_ = json.NewDecoder(res.Body).Decode(&reply)
		if res.StatusCode != 403 && res.Header.Get("Content-Type") != "application/json" {
			t.Errorf("reply is %s", res.Header.Get("Content-Type"))
		}
		return res.StatusCode, reply
	}
	if code, reply := post(url.Values{"theme": {"dark"}}, private.URL); code != 200 || reply["ok"] != true || reply["theme"] != "dark" || reply["message"] != "Dark is on everywhere, shared pages included." {
		t.Fatal(code, reply)
	}
	if _, theme, _ := themeOf(t, private, "/"); theme != "dark" {
		t.Fatal("fetch did not save", theme)
	}
	if code, reply := post(url.Values{"theme": {"sepia"}}, private.URL); code != 400 || reply["ok"] != false || reply["message"] != "Choose one of the listed themes." {
		t.Fatal(code, reply)
	}
	if code, _ := post(url.Values{"theme": {"cairn"}}, "https://elsewhere.example"); code != 403 {
		t.Fatal("foreign theme post", code)
	}
	if _, theme, _ := themeOf(t, private, "/"); theme != "dark" {
		t.Fatal("refused posts changed the theme", theme)
	}
}

// A theme ID this build doesn't know, from a newer or damaged database, falls
// back instead of breaking pages.
func TestUnknownSavedThemesFallBack(t *testing.T) {
	dir := t.TempDir()
	private, public, closeApp := openThemed(t, dir)
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Shared"}, "markdown": {"Text."}, "slug": {"shared"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	closeApp()
	db := rawDB(t, dir)
	if _, err := db.Exec(`UPDATE theme_preferences SET admin='neon',reader='sepia',public='dark'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	private, public, closeApp = openThemed(t, dir)
	defer closeApp()
	if _, theme, _ := themeOf(t, private, "/"); theme != "cairn" {
		t.Fatal("unknown library theme", theme)
	}
	if _, theme, _ := themeOf(t, private, "/shared"); theme != "cairn" {
		t.Fatal("unknown reader theme should follow the library", theme)
	}
	if _, theme, _ := themeOf(t, public, "/shared"); theme != "dark" {
		t.Fatal("known shared theme lost", theme)
	}
	if _, _, page := themeOf(t, private, "/settings"); !strings.Contains(page, `value="cairn" checked`) {
		t.Fatal("settings should show the theme in use")
	}
}

// Installations from before themes followed the device keep what they saved,
// and views that matched the library now follow it.
func TestExistingInstallationsKeepTheirThemes(t *testing.T) {
	dir := t.TempDir()
	db := rawDB(t, dir)
	if _, err := db.Exec(`CREATE TABLE theme_preferences (id INTEGER PRIMARY KEY CHECK(id=1),admin TEXT NOT NULL DEFAULT 'cairn',reader TEXT NOT NULL DEFAULT 'cairn',public TEXT NOT NULL DEFAULT 'cairn');
 INSERT INTO theme_preferences VALUES(1,'minimal','minimal','futuristic');`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	private, public, closeApp := openThemed(t, dir)
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Shared"}, "markdown": {"Text."}, "slug": {"shared"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	for server, want := range map[*httptest.Server][2]string{private: {"/shared", "minimal"}, public: {"/shared", "futuristic"}} {
		if _, theme, _ := themeOf(t, server, want[0]); theme != want[1] {
			t.Fatal("saved theme lost", want, theme)
		}
	}
	// The reader matched the library, so a new library theme reaches it;
	// shared pages had their own and keep it.
	if code, _, _ := authorForm(t, private, "/settings", url.Values{"action": {"themes"}, "theme": {"dark"}, "reader_theme": {""}, "public_theme": {"futuristic"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	closeApp()
	private, public, closeApp = openThemed(t, dir)
	defer closeApp()
	if _, theme, _ := themeOf(t, private, "/shared"); theme != "dark" {
		t.Fatal("reader should follow the library", theme)
	}
	if _, theme, _ := themeOf(t, public, "/shared"); theme != "futuristic" {
		t.Fatal("shared theme lost", theme)
	}
}

// System follows the device everywhere, visitors' devices included, without
// Cairn storing anything about them; every page names its colour scheme.
func TestSystemThemeFollowsEachDevice(t *testing.T) {
	private, public, closeApp := openThemed(t, t.TempDir())
	defer closeApp()
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Shared"}, "markdown": {"# Shared\n\nText."}, "slug": {"shared"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	for _, server := range []*httptest.Server{private, public} {
		code, h, body := request(t, server, "GET", "/shared", nil, "")
		page := string(body)
		if code != 200 || !strings.Contains(page, `<meta name="color-scheme" content="light dark">`) || !strings.Contains(page, `<meta name="theme-color" content="#eef2f5" media="(prefers-color-scheme: light)">`) || !strings.Contains(page, `<meta name="theme-color" content="#111b22" media="(prefers-color-scheme: dark)">`) {
			t.Fatal("system page does not follow the device")
		}
		if !strings.Contains(page, `src="/_content/shared?scheme=auto"`) {
			t.Fatal("report frame does not follow the device")
		}
		if h.Get("Set-Cookie") != "" || h.Get("Vary") != "" && strings.Contains(h.Get("Vary"), "Sec-CH-Prefers-Color-Scheme") {
			t.Fatal("system theme stored or asked something about the visitor")
		}
	}
	_, _, content := request(t, public, "GET", "/_content/shared?scheme=auto", nil, "")
	if !strings.Contains(string(content), `<html data-report-scheme="auto" lang="en">`) || !strings.Contains(string(content), `@media (prefers-color-scheme:dark){:root[data-report-scheme="auto"]`) {
		t.Fatal("report cannot follow the device")
	}
	for theme, scheme := range map[string]string{"cairn": "light", "minimal": "light", "dark": "dark", "futuristic": "dark"} {
		if code, _, _ := authorForm(t, private, "/settings", url.Values{"action": {"themes"}, "theme": {theme}}, private.URL); code != 303 {
			t.Fatal(code)
		}
		_, _, body := request(t, private, "GET", "/", nil, "")
		if !strings.Contains(string(body), `<meta name="color-scheme" content="`+scheme+`">`) {
			t.Fatal(theme, "color-scheme")
		}
	}
}

// Pages Cairn rendered take the theme's colours from their first frame; pages
// in their own design keep it and sit on the page as a sheet.
func TestReportsFollowTheTheme(t *testing.T) {
	private, public, closeApp := openThemed(t, t.TempDir())
	defer closeApp()
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Written"}, "markdown": {"# Written\n\nText."}, "slug": {"written"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	custom := page("Custom")
	custom["slug"] = "custom"
	custom["html"] = `<!doctype html><html lang="en"><head><title>Custom</title><style>body{background:#fbf7f0}</style></head><body><h1>Custom</h1></body></html>`
	if code, _, body := request(t, private, "POST", "/api/pages", custom, token); code != 201 {
		t.Fatal(code, string(body))
	}
	cases := []struct{ theme, src, attrs string }{
		{"cairn", `src="/_content/written"`, ""},
		// Minimal's reports are black on white too.
		{"minimal", `src="/_content/written?scheme=light&amp;palette=mono"`, ` data-report-scheme="light" data-report-palette="mono"`},
		{"dark", `src="/_content/written?scheme=dark"`, ` data-report-scheme="dark"`},
		{"futuristic", `src="/_content/written?scheme=dark&amp;palette=violet"`, ` data-report-scheme="dark" data-report-palette="violet"`},
	}
	for _, c := range cases {
		if code, _, _ := authorForm(t, private, "/settings", url.Values{"action": {"themes"}, "theme": {c.theme}}, private.URL); code != 303 {
			t.Fatal(code)
		}
		for _, server := range []*httptest.Server{private, public} {
			_, _, body := request(t, server, "GET", "/written", nil, "")
			if !strings.Contains(string(body), c.src) || strings.Contains(string(body), "report-fixed") {
				t.Fatal(c.theme, "reader frame", string(body))
			}
			src := strings.ReplaceAll(regexp.MustCompile(`src="(/_content/[^"]+)"`).FindStringSubmatch(string(body))[1], "&amp;", "&")
			_, _, content := request(t, server, "GET", src, nil, "")
			if !strings.Contains(string(content), `<html`+c.attrs+` lang="en">`) {
				t.Fatal(c.theme, "report scheme", src)
			}
			_, _, body = request(t, private, "GET", "/custom", nil, "")
			if !strings.Contains(string(body), " report-fixed") {
				t.Fatal("custom HTML should keep its own design")
			}
		}
		// The editor's preview takes the library's theme.
		_, _, newPage := request(t, private, "GET", "/new", nil, "")
		csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(newPage)[1]
		req, _ := http.NewRequest("POST", private.URL+"/new?preview=1", strings.NewReader(url.Values{"csrf": {string(csrf)}, "title": {"Draft"}, "markdown": {"# Draft\n\nText."}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", private.URL)
		res, err := private.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		preview := new(strings.Builder)
		_, _ = io.Copy(preview, res.Body)
		res.Body.Close()
		if !strings.Contains(preview.String(), `<html`+c.attrs+` lang="en">`) {
			t.Fatal(c.theme, "editor preview scheme")
		}
	}
	// Only known schemes reach the page.
	for _, bad := range []string{"?scheme=%22%3E%3Cscript%3E", "?scheme=sepia", "?scheme=light&palette=violet"} {
		_, _, content := request(t, private, "GET", "/_content/written"+bad, nil, "")
		if strings.Contains(string(content), `<html data-report`) || strings.Contains(string(content), `"><script>`) {
			t.Fatal("unknown scheme reached the report", bad)
		}
	}
	// The stylesheet defines what the attributes switch.
	css, err := os.ReadFile(filepath.Join("report.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`:root[data-report-scheme="dark"] {`, `:root[data-report-scheme="dark"][data-report-palette="violet"] {`, `:root[data-report-scheme="auto"] {`} {
		if !strings.Contains(string(css), want) {
			t.Fatal("report.css lacks", want)
		}
	}
}

// The System blocks must stay copies of the Dark blocks they stand for.
func TestSystemBlocksMatchDark(t *testing.T) {
	block := func(css, selector string) string {
		t.Helper()
		i := strings.Index(css, selector)
		if i < 0 {
			t.Fatal("missing", selector)
		}
		body := css[strings.Index(css[i:], "{")+i+1:]
		body = body[:strings.Index(body, "}")]
		return strings.Join(strings.Fields(body), " ")
	}
	tokens, _ := os.ReadFile(filepath.Join("ui", "tokens.css"))
	if dark, system := block(string(tokens), `:root[data-theme="dark"],`), block(string(tokens), `:root[data-theme="system"] {`); dark != system || !strings.Contains(dark, "--bg: #111b22") {
		t.Fatalf("tokens.css: the System dark block differs from Dark\n%s\n%s", dark, system)
	}
	report, _ := os.ReadFile("report.css")
	if dark, auto := block(string(report), `:root[data-report-scheme="dark"] {`), block(string(report), `:root[data-report-scheme="auto"] {`); dark != auto {
		t.Fatalf("report.css: the auto block differs from dark\n%s\n%s", dark, auto)
	}
}
