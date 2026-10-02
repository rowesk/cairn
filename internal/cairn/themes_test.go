package cairn_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

func TestThemeSelectionsPersistAndStayInTheirViews(t *testing.T) {
	dir := t.TempDir()
	open := func() (*httptest.Server, *httptest.Server, func()) {
		app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		private := httptest.NewServer(app)
		public := httptest.NewServer(app.PublicHandler())
		return private, public, func() { private.Close(); public.Close(); app.Close() }
	}
	private, public, closeApp := open()
	defer func() { closeApp() }()
	assertTheme := func(server *httptest.Server, path, theme string, status int) {
		t.Helper()
		code, _, body := request(t, server, "GET", path, nil, "")
		if code != status || !strings.Contains(string(body), `<html lang="en" data-theme="`+theme+`">`) {
			t.Fatalf("%s: wanted status %d and theme %s, got status %d", path, status, theme, code)
		}
	}
	assertTheme(private, "/settings", "cairn", 200)
	if code, _, _ := authorForm(t, private, "/new", url.Values{"title": {"Theme report"}, "markdown": {"Original report content"}, "slug": {"theme-report"}, "access": {"public"}}, private.URL); code != 303 {
		t.Fatal("create report", code)
	}
	_, _, originalContent := request(t, private, "GET", "/_content/theme-report", nil, "")
	_, _, originalSource := request(t, private, "GET", "/theme-report?download=1", nil, "")
	choices := url.Values{"action": {"themes"}, "admin_theme": {"minimal"}, "reader_theme": {"futuristic"}, "public_theme": {"cairn"}}
	if code, _, _ := authorForm(t, private, "/settings", choices, "https://elsewhere.example"); code != 403 {
		t.Fatal("cross-origin theme write accepted", code)
	}
	assertTheme(private, "/settings", "cairn", 200)
	if code, _, _ := authorForm(t, private, "/settings", choices, private.URL); code != 303 {
		t.Fatal("save themes", code)
	}
	for _, path := range []string{"/", "/settings", "/new", "/import", "/manage/theme-report", "/manage/theme-report/delete"} {
		assertTheme(private, path, "minimal", 200)
	}
	assertTheme(private, "/theme-report", "futuristic", 200)
	assertTheme(public, "/theme-report", "cairn", 200)
	for path, original := range map[string][]byte{"/_content/theme-report": originalContent, "/theme-report?download=1": originalSource} {
		for _, server := range []*httptest.Server{private, public} {
			_, _, body := request(t, server, "GET", path, nil, "")
			if string(body) != string(original) {
				t.Fatalf("themes changed report content or source at %s", path)
			}
		}
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
	// Older preferences actions must not reset themes.
	if code, _, _ := authorForm(t, private, "/settings", url.Values{"action": {"defaults"}, "author": {"Owner"}, "template": {"report"}, "link_length": {"3"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	closeApp()
	private, public, closeApp = open()
	assertTheme(private, "/settings", "minimal", 200)
	assertTheme(private, "/theme-report", "futuristic", 200)
	assertTheme(public, "/theme-report", "cairn", 200)
	for _, key := range []string{"admin_theme", "reader_theme", "public_theme"} {
		choices.Set(key, "futuristic")
	}
	if code, _, _ := authorForm(t, private, "/settings", choices, private.URL); code != 303 {
		t.Fatal(code)
	}
	assertTheme(private, "/", "futuristic", 200)
	assertTheme(private, "/theme-report", "futuristic", 200)
	assertTheme(public, "/theme-report", "futuristic", 200)
	if code, _, _ := authorForm(t, private, "/manage/theme-report", url.Values{"action": {"password"}, "password": {"secret"}}, private.URL); code != 303 {
		t.Fatal(code)
	}
	assertTheme(public, "/theme-report", "futuristic", 401)
	if code, _, _ := request(t, public, "GET", "/settings", nil, ""); code != 404 {
		t.Fatal("settings exposed publicly", code)
	}
}
