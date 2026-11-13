package cairn_test

import (
	"bytes"
	"io"
	"mime/multipart"
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

// postForm posts a urlencoded form exactly as given: the caller chooses the
// token and the Origin, so stale and foreign posts can be tested.
func postForm(t *testing.T, srv *httptest.Server, path string, values url.Values, origin string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(body)
}

// postImport sends the import form with any fields, an optional file and the
// given token.
func postImport(t *testing.T, srv *httptest.Server, fields map[string]string, name string, data []byte, csrf string) (int, string) {
	t.Helper()
	var b bytes.Buffer
	f := multipart.NewWriter(&b)
	f.WriteField("csrf", csrf)
	for k, v := range fields {
		f.WriteField(k, v)
	}
	if name != "" {
		w, _ := f.CreateFormFile("document", name)
		w.Write(data)
	}
	f.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/import", &b)
	req.Header.Set("Content-Type", f.FormDataContentType())
	req.Header.Set("Origin", srv.URL)
	c := *srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func formToken(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, _, body := request(t, srv, "GET", "/new", nil, "")
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(body)
	if len(m) != 2 {
		t.Fatal("no form token")
	}
	return string(m[1])
}

// themed reports whether a body is Cairn's own HTML page rather than text.
func themed(body string) bool {
	return strings.HasPrefix(body, "<!doctype html>") && strings.Contains(body, `<main id="main"`) && strings.Contains(body, `<link rel="stylesheet" href="/_ui/cairn.`)
}

var inlineScript = regexp.MustCompile(`(?s)<script>.*?</script>`)

// markup is a page without its inline script, whose source names
// attributes and words the page itself may not use.
func markup(body string) string { return inlineScript.ReplaceAllString(body, "") }

// visitorLeaks lists what a visitor page must never contain.
func visitorLeaks(body string) []string {
	var leaks []string
	for _, private := range []string{`href="/"`, "/manage/", "/settings", "/import", `name="csrf"`, "topbar-back", "Your library", "your library", "PRIVATE:", "Original request"} {
		if strings.Contains(body, private) {
			leaks = append(leaks, private)
		}
	}
	return leaks
}

func TestBrowserErrorsAreThemedOnBothListeners(t *testing.T) {
	private, public := startShared(t)
	if status, _, body := request(t, private, "POST", "/api/pages", page("Secret plans"), token); status != 201 {
		t.Fatal(status, string(body))
	}
	in := page("Protected page title")
	in["slug"] = "locked"
	if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatal(status, string(body))
	}
	for _, v := range []url.Values{{"action": {"share"}}, {"action": {"password"}, "password": {"long enough"}}} {
		if code := ownerPost(t, private, "locked", v); code != 303 {
			t.Fatal(code)
		}
	}
	// Owner: every browser route answers with the owner page and its way back.
	for _, tc := range []struct {
		method, path string
		status       int
		says         string
	}{
		{"GET", "/nope", 404, "This page isn’t in your library"},
		{"GET", "/manage/nope", 404, "This page isn’t in your library"},
		{"GET", "/manage/bad%20slug", 404, "This page isn’t in your library"},
		{"GET", "/_assets/report/missing.png", 404, "This page isn’t in your library"},
		{"GET", "/_content/report?asset=missing.png", 404, "This page isn’t in your library"},
		{"GET", "/report?download=1", 404, "This page isn’t in your library"},
		{"POST", "/report", 405, "Cairn can’t do that here"},
		{"PUT", "/settings", 405, "Cairn can’t do that here"},
		{"DELETE", "/import", 405, "Cairn can’t do that here"},
	} {
		res, body := fetch(t, private, tc.method, tc.path, nil)
		if res.StatusCode != tc.status || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !themed(string(body)) || !strings.Contains(string(body), tc.says) {
			t.Errorf("owner %s %s: %d %s\n%s", tc.method, tc.path, res.StatusCode, res.Header.Get("Content-Type"), body)
		}
		// One way back: the page's own button. The top bar adds no second one.
		if !strings.Contains(string(body), `<a class="button" href="/">Back to library</a>`) || strings.Contains(string(body), "back-link") {
			t.Errorf("owner %s has no way back, or two", tc.path)
		}
		if tc.status == 405 && res.Header.Get("Allow") == "" {
			t.Errorf("%s 405 without Allow", tc.path)
		}
	}
	// A post from another site is refused with the owner page, input unshown.
	code, _, body := postForm(t, private, "/manage/report", url.Values{"csrf": {formToken(t, private)}, "action": {"rename"}, "title": {"Hijacked title"}}, "https://elsewhere.example")
	if code != 403 || !themed(body) || strings.Contains(body, "Hijacked title") || !strings.Contains(body, "Cairn didn’t accept that request") {
		t.Errorf("foreign post: %d %s", code, body)
	}
	// Visitor: the same page in the visitor shell, saying nothing private.
	var notFound string
	for _, tc := range []struct {
		method, path string
		status       int
		says         string
	}{
		{"GET", "/nope", 404, "This link isn’t working"},
		{"GET", "/", 404, "This link isn’t working"},
		{"GET", "/report", 404, "This link isn’t working"},
		{"GET", "/manage/report", 404, "This link isn’t working"},
		{"GET", "/settings", 404, "This link isn’t working"},
		{"GET", "/api", 404, "This link isn’t working"},
		{"GET", "/_content/report", 404, "This link isn’t working"},
		{"DELETE", "/locked", 404, "This link isn’t working"},
		{"GET", "/_content/locked", 401, "This page is protected by a password"},
		{"GET", "/locked?download=1", 404, "This link isn’t working"},
	} {
		res, body := fetch(t, public, tc.method, tc.path, nil)
		page := string(body)
		if res.StatusCode != tc.status || !themed(page) || !strings.Contains(page, tc.says) || !strings.Contains(page, `<body class="visitor`) {
			t.Errorf("visitor %s %s: %d\n%s", tc.method, tc.path, res.StatusCode, page)
		}
		if leaks := visitorLeaks(page); len(leaks) > 0 || strings.Contains(page, "Secret plans") || strings.Contains(page, "Protected page title") {
			t.Errorf("visitor %s leaks %v", tc.path, leaks)
		}
		if tc.status == 404 {
			// A private page and a missing one answer identically.
			if notFound == "" {
				notFound = page
			} else if page != notFound {
				t.Errorf("visitor 404 for %s differs from other 404s", tc.path)
			}
		}
	}
	// A password post from elsewhere is refused in the visitor shell.
	code, _, body = postForm(t, public, "/locked", url.Values{"password": {"long enough"}}, "https://elsewhere.example")
	if code != 403 || !themed(body) || len(visitorLeaks(body)) > 0 || !strings.Contains(body, "Enter the password on the page itself") {
		t.Errorf("foreign unlock: %d %s", code, body)
	}
	// The JSON API keeps its own shape.
	res, apiBody := fetch(t, private, "GET", "/api/nope", nil)
	if res.StatusCode != 404 || res.Header.Get("Content-Type") != "application/json" || !strings.Contains(string(apiBody), `"code":"not_found"`) {
		t.Errorf("API 404 changed: %s %s", res.Header.Get("Content-Type"), apiBody)
	}
}

func TestFormTokenSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	srv, closeServer := start(t, dir)
	before := formToken(t, srv)
	info, err := os.Stat(filepath.Join(dir, "form.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("form key: %v %v", info, err)
	}
	closeServer()
	srv, closeServer = start(t, dir)
	defer closeServer()
	// Pages mask the token afresh each time (BREACH), so two renders never
	// match, yet both carry the same secret.
	if after := formToken(t, srv); after == before || len(after) != 128 {
		t.Fatalf("the form token is not masked per page: %q then %q", before, after)
	}
	// A form opened before the restart still saves.
	code, h, body := postForm(t, srv, "/new", url.Values{"csrf": {before}, "title": {"Written across a restart"}, "markdown": {"Still here."}}, srv.URL)
	if code != 303 || h.Get("Location") == "" {
		t.Fatalf("old form after restart: %d %s", code, body)
	}
	// A damaged key stops startup with a message that says what to do.
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "form.key"), []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cairn.Open(cairn.Config{DataDir: broken, PublisherToken: token}); err == nil || !strings.Contains(err.Error(), "form.key is damaged") {
		t.Fatalf("damaged key: %v", err)
	}
}

func TestStaleTokenShowsTheFormAgainWithInput(t *testing.T) {
	private, _ := startShared(t)
	if status, _, body := request(t, private, "POST", "/api/pages", page("Bulb comparison"), token); status != 201 {
		t.Fatal(status, string(body))
	}
	stale := url.Values{"csrf": {"from-an-older-key"}}
	with := func(extra url.Values) url.Values {
		v := url.Values{}
		for k, vs := range stale {
			v[k] = vs
		}
		for k, vs := range extra {
			v[k] = vs
		}
		return v
	}
	for _, tc := range []struct {
		path   string
		values url.Values
		kept   []string
	}{
		{"/new", url.Values{"title": {"Unsaved idea"}, "markdown": {"Words worth keeping"}, "slug": {"my-idea"}}, []string{">Unsaved idea</textarea>", "Words worth keeping", `value="my-idea"`}},
		{"/manage/report", url.Values{"action": {"rename"}, "title": {"Better title"}}, []string{">Better title</textarea>"}},
		{"/manage/report", url.Values{"action": {"share"}, "public_slug": {"bulbs"}}, []string{"so sharing didn’t change"}},
		{"/manage/report/delete", url.Values{"action": {"delete"}}, []string{"the page wasn’t deleted", `<details class="delete-confirm" open>`}},
		{"/settings", url.Values{"action": {"defaults"}, "author": {"New author"}, "template": {"visual"}, "link_length": {"6"}}, []string{`value="New author"`, `value="6"`, `<option value="visual" selected>`}},
		{"/settings", url.Values{"action": {"kindle"}, "kindle_address": {"me@kindle.com"}}, []string{`value="me@kindle.com"`}},
	} {
		code, _, body := postForm(t, private, tc.path, with(tc.values), private.URL)
		if code != 403 || !strings.Contains(body, "had expired") || !strings.Contains(body, `class="toast is-info"`) {
			t.Errorf("%s %v: %d\n%s", tc.path, tc.values, code, body)
			continue
		}
		for _, want := range tc.kept {
			if !strings.Contains(body, want) {
				t.Errorf("%s lost %q", tc.path, want)
			}
		}
		if !regexp.MustCompile(`name="csrf" value="[0-9a-f]{128}"`).MatchString(body) {
			t.Errorf("%s did not hand out a fresh token", tc.path)
		}
	}
	// Nothing changed.
	_, _, body := request(t, private, "GET", "/manage/report", nil, "")
	if !strings.Contains(string(body), ">Bulb comparison</textarea>") {
		t.Fatal("stale rename applied")
	}
	if code, _, _ := request(t, private, "GET", "/my-idea", nil, ""); code != 404 {
		t.Fatal("stale editor post saved")
	}
	// Import keeps its fields and asks for the file again.
	code, out := postImport(t, private, map[string]string{"title": "Imported notes", "agent": "Codex", "original_request": "Keep my request"}, "notes.md", []byte("# Notes"), "from-an-older-key")
	if code != 403 || !strings.Contains(out, "nothing was imported") || !strings.Contains(out, `value="Imported notes"`) || !strings.Contains(out, ">Keep my request</textarea>") || !strings.Contains(out, `value="Codex"`) {
		t.Errorf("stale import: %d\n%s", code, out)
	}
	// One-button forms go back with a notice instead.
	code, h, _ := postForm(t, private, "/manage/report", with(url.Values{"action": {"kindle"}}), private.URL)
	if code != 303 || h.Get("Location") != "/?kindle=expired&page=report" {
		t.Errorf("stale Kindle send: %d %s", code, h.Get("Location"))
	}
	// The key dialog reads a plain sentence.
	code, h, out = postForm(t, private, "/settings", with(url.Values{"action": {"key_create"}, "name": {"Codex"}}), private.URL)
	if code != 403 || !strings.HasPrefix(h.Get("Content-Type"), "text/plain") || !strings.Contains(out, "Reload Settings") {
		t.Errorf("stale key: %d %s %s", code, h.Get("Content-Type"), out)
	}
}

func TestFormsShowValidationBesideTheField(t *testing.T) {
	private, _ := startShared(t)
	for _, slug := range []string{"report", "taken"} {
		in := page("Bulb comparison")
		in["slug"] = slug
		if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
			t.Fatal(status, string(body))
		}
	}
	if code := ownerPost(t, private, "taken", url.Values{"action": {"share"}, "public_slug": {"bulbs"}}); code != 303 {
		t.Fatal(code)
	}
	field := func(id string) []string {
		return []string{`id="` + id + `" role="alert"`, `aria-invalid="true"`, `aria-describedby="` + id}
	}
	csrf := formToken(t, private)
	for _, tc := range []struct {
		name, path string
		values     url.Values
		status     int
		want       []string
	}{
		{"editor title", "/new", url.Values{"title": {""}, "markdown": {"Keep my writing"}}, 400, append(field("page-title-error"), "Give the page a title.", "Keep my writing")},
		{"editor body", "/new", url.Values{"title": {"A title"}, "markdown": {"  "}}, 400, append(field("markdown-error"), ">A title</textarea>")},
		{"editor taken link", "/new", url.Values{"title": {"A title"}, "markdown": {"Words"}, "slug": {"report"}}, 409, append(field("page-slug-error"), "Another page already uses this link", `value="report"`, "Words", `class="toast is-error"`)},
		{"editor reserved link", "/new", url.Values{"title": {"A title"}, "markdown": {"Words"}, "slug": {"settings"}}, 400, append(field("page-slug-error"), "Cairn uses “settings” itself")},
		{"editor fake image", "/new", url.Values{"title": {"A title"}, "markdown": {"Words"}, "slug": {"fine-link"}, "images": {`{"x.png":"PD9waHA="}`}}, 400, []string{"Images must be PNG, JPEG, GIF or WebP", "Words"}},
		{"rename empty", "/manage/report", url.Values{"action": {"rename"}, "title": {"   "}}, 400, append(field("page-title-error"), "Give the page a title.")},
		{"rename long", "/manage/report", url.Values{"action": {"rename"}, "title": {strings.Repeat("Long ", 70)}}, 400, append(field("page-title-error"), "Shorten the title to 300 characters or fewer.")},
		{"link taken while off", "/manage/report", url.Values{"action": {"share"}, "public_slug": {"bulbs"}}, 409, append(field("link-name-error"), linkTaken, `name="public_slug" value="bulbs"`, "Share at this link")},
		{"link invalid", "/manage/report", url.Values{"action": {"share"}, "public_slug": {"no spaces"}}, 400, append(field("link-name-error"), "Use letters, numbers, dashes or underscores", `value="no spaces"`)},
		{"settings defaults", "/settings", url.Values{"action": {"defaults"}, "author": {"Keep this author"}, "template": {"report"}, "link_length": {"9"}}, 400, append(field("link-length-error"), "Choose a length from 3 to 7 characters.", `value="9"`, `value="Keep this author"`)},
		{"settings author", "/settings", url.Values{"action": {"defaults"}, "author": {""}, "template": {"report"}, "link_length": {"4"}}, 400, append(field("default-author-error"), `value="4"`)},
		{"settings kindle", "/settings", url.Values{"action": {"kindle"}, "kindle_address": {"not an address"}}, 400, append(field("kindle-address-error"), `value="not an address"`, "Enter one email address")},
	} {
		tc.values.Set("csrf", csrf)
		code, _, body := postForm(t, private, tc.path, tc.values, private.URL)
		if code != tc.status {
			t.Errorf("%s: status %d, want %d\n%s", tc.name, code, tc.status, body)
			continue
		}
		if !themed(body) {
			t.Errorf("%s: not the form page", tc.name)
		}
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", tc.name, want)
			}
		}
	}
	// Once shared, the same check sits beside the link name field.
	if code := ownerPost(t, private, "report", url.Values{"action": {"share"}}); code != 303 {
		t.Fatal(code)
	}
	if code, _, page := postForm(t, private, "/manage/report", url.Values{"csrf": {csrf}, "action": {"share"}, "public_slug": {"bulbs"}}, private.URL); code != 409 || !strings.Contains(page, linkTaken) || !strings.Contains(page, "Save sharing") || !strings.Contains(page, `id="link-name-error" role="alert"`) {
		t.Errorf("shared link taken: %d", code)
	}
	// Import: errors beside the file and the details, input kept.
	fields := map[string]string{"title": "Imported title", "agent": "Codex", "original_request": "Why I kept it", "template": "comparison", "slug": "taken"}
	code, body := postImport(t, private, fields, "notes.md", []byte("# Notes"), csrf)
	for _, want := range append(field("import-slug-error"), linkTaken, `value="Imported title"`, `value="Codex"`, ">Why I kept it</textarea>", `<option value="comparison" selected>`, `<details class="more-fields" open>`, "Choose “notes.md” again") {
		if code != 409 || !strings.Contains(body, want) {
			t.Errorf("import taken link: %d missing %q", code, want)
		}
	}
	code, body = postImport(t, private, map[string]string{"title": "Kept"}, "tool.exe", []byte("MZ"), csrf)
	if code != 303 {
		t.Errorf("inert file import: %d", code)
	}
	_, _, form := request(t, private, "GET", "/import", nil, "")
	if !strings.Contains(string(form), `data-too-large="The file must fit 512 MiB. Choose a smaller one."`) || !strings.Contains(string(form), "Any file up to 512 MiB.") {
		t.Error("drop zone lacks file limits")
	}
	code, body = postImport(t, private, map[string]string{"title": "No file"}, "", nil, csrf)
	if code != 400 || !strings.Contains(body, "Choose a file to import.") || !strings.Contains(body, `value="No file"`) {
		t.Errorf("missing file: %d", code)
	}
	tags := regexp.MustCompile(`(?s)<script>.*?</script>|<[^>]+>`)
	for _, page := range []string{string(form), body} {
		text := tags.ReplaceAllString(page, " ")
		if strings.Contains(text, "bytes") || strings.Contains(text, "slug") || strings.Contains(text, "PHP") {
			t.Errorf("import still speaks in jargon: %s", text)
		}
	}
}

const linkTaken = "Another page already uses this link. Choose a different one."

func TestSharePasswordNeedsEightCharacters(t *testing.T) {
	private, _ := startShared(t)
	if status, _, body := request(t, private, "POST", "/api/pages", page("Bulb comparison"), token); status != 201 {
		t.Fatal(status, string(body))
	}
	if code := ownerPost(t, private, "report", url.Values{"action": {"share"}}); code != 303 {
		t.Fatal(code)
	}
	for _, tc := range []struct {
		password string
		status   int
		says     string
	}{
		{"", 400, "Use at least 8 characters."},
		{"seven77", 400, "Use at least 8 characters."},
		{"éééééé", 400, "Use at least 8 characters."},
		{strings.Repeat("a", 73), 400, "That password is too long. Use 72 characters or fewer."},
		{"eight888", 303, ""},
	} {
		code, _, body := authorForm(t, private, "/manage/report", url.Values{"action": {"password"}, "password": {tc.password}}, private.URL)
		if code != tc.status || (tc.says != "" && !strings.Contains(body, tc.says)) {
			t.Errorf("password %q: %d\n%s", tc.password, code, body)
		}
		if tc.status != 303 && (strings.Contains(body, tc.password) && tc.password != "") {
			t.Errorf("password %q echoed back", tc.password)
		}
		if strings.Contains(markup(body), "bytes") {
			t.Error("password rule mentions bytes")
		}
	}
}

func TestSharingControlConfirmsEachChangeInPlace(t *testing.T) {
	private, public := startShared(t)
	if status, _, body := request(t, private, "POST", "/api/pages", page("Bulb comparison"), token); status != 201 {
		t.Fatal(status, string(body))
	}
	view := func() string {
		_, _, body := request(t, private, "GET", "/manage/report", nil, "")
		return string(body)
	}
	if body := view(); !strings.Contains(body, `role="switch" aria-checked="false"`) || !strings.Contains(body, `name="action" value="share"`) || !strings.Contains(body, "publish it at reports.example.com/report") || strings.Contains(body, `id="public-address"`) {
		t.Fatalf("private state:\n%s", body)
	}
	steps := []struct {
		values   url.Values
		location string
		says     string
		state    []string
	}{
		{url.Values{"action": {"share"}}, "/manage/report?done=shared#sharing", "Shared. Anyone with the link can read this page.", []string{`aria-checked="true"`, `<strong>Shared.</strong>`, `name="public_slug" value="report"`, `data-copy-value="https://reports.example.com/report"`, "Save sharing", `Password <span class="field-optional">Optional</span>`}},
		{url.Values{"action": {"share"}, "public_slug": {"bulbs"}}, "/manage/report?done=relinked#sharing", "Link name saved.", []string{`data-copy-value="https://reports.example.com/bulbs"`}},
		{url.Values{"action": {"share"}, "public_slug": {"lamps"}}, "/manage/report?done=relinked-old#sharing", "Link name saved. The old link no longer works.", []string{`data-copy-value="https://reports.example.com/lamps"`}},
		{url.Values{"action": {"password"}, "password": {"first secret"}}, "/manage/report?done=password-set#sharing", "Password set. Visitors need it to read this page.", []string{"<strong>Shared with a password.</strong>", "Remove password", `class="field-badge"`, `id="copy-both"`}},
		{url.Values{"action": {"password"}, "password": {"second secret"}}, "/manage/report?done=password-changed#sharing", "Password changed.", []string{"<strong>Shared with a password.</strong>"}},
		{url.Values{"action": {"remove_password"}}, "/manage/report?done=password-removed#sharing", "Password removed. Anyone with the link can read this page.", []string{"<strong>Shared.</strong>"}},
		// The section's one Save takes a new link name and a new password together.
		{url.Values{"action": {"sharing"}, "public_slug": {"lamps"}, "password": {"third secret"}}, "/manage/report?done=password-set#sharing", "Password set.", []string{"<strong>Shared with a password.</strong>"}},
		{url.Values{"action": {"sharing"}, "public_slug": {"lamps"}}, "/manage/report?done=unchanged#sharing", "Nothing to save", []string{`data-copy-value="https://reports.example.com/lamps"`}},
		{url.Values{"action": {"unshare"}}, "/manage/report?done=unshared#sharing", "Sharing is off. The public link no longer works.", []string{`aria-checked="false"`, "publish it at reports.example.com/lamps"}},
		{url.Values{"action": {"archive"}}, "/manage/report?done=archived#archive", "Archived. The page is hidden from your library list", []string{"Restore to library", `<div class="toast-actions">`, `<input type="hidden" name="action" value="restore">`, `<button>Undo</button></form>`}},
		{url.Values{"action": {"restore"}}, "/manage/report?done=restored#archive", "Restored.", []string{"Archive page"}},
		{url.Values{"action": {"rename"}, "title": {"Lamps, \r\nranked"}}, "/manage/report?done=renamed#title", "Title saved.", []string{">Lamps, ranked</textarea>"}},
	}
	for _, step := range steps {
		code, h, body := authorForm(t, private, "/manage/report", step.values, private.URL)
		if code != 303 || h.Get("Location") != step.location {
			t.Fatalf("%v: %d %s\n%s", step.values, code, h.Get("Location"), body)
		}
		_, _, page := request(t, private, "GET", step.location, nil, "")
		if !strings.Contains(string(page), step.says) || !strings.Contains(string(page), `class="toast is-success" data-toast`) {
			t.Errorf("%v: no confirmation %q", step.values, step.says)
		}
		for _, want := range step.state {
			if !strings.Contains(string(page), want) {
				t.Errorf("%v: state lacks %q", step.values, want)
			}
		}
	}
	if code, _, _ := request(t, public, "GET", "/lamps", nil, ""); code != 404 {
		t.Error("unshared page still public")
	}
	// Deleting goes back to the library, which says what went.
	code, h, _ := authorForm(t, private, "/manage/report/delete", url.Values{"action": {"delete"}}, private.URL)
	// The address names a short-lived flash, never the title.
	if code != 303 || !regexp.MustCompile(`^/\?deleted=[0-9a-f]{16}$`).MatchString(h.Get("Location")) {
		t.Fatalf("delete: %d %s", code, h.Get("Location"))
	}
	_, _, library := request(t, private, "GET", h.Get("Location"), nil, "")
	if !strings.Contains(string(library), "Deleted “Lamps, ranked”.") {
		t.Error("library does not confirm the deletion")
	}
	// A delete outside its confirmation goes to the confirmation.
	request(t, private, "POST", "/api/pages", page("Second"), token)
	code, h, _ = authorForm(t, private, "/manage/report", url.Values{"action": {"delete"}}, private.URL)
	if code != 303 || h.Get("Location") != "/manage/report/delete#delete" {
		t.Errorf("unconfirmed delete: %d %s", code, h.Get("Location"))
	}
	// The Settings page confirms each section in its own place.
	code, h, _ = authorForm(t, private, "/settings", url.Values{"action": {"themes"}, "admin_theme": {"cairn"}, "reader_theme": {"cairn"}, "public_theme": {"minimal"}}, private.URL)
	if code != 303 || h.Get("Location") != "/settings?saved=appearance#appearance" {
		t.Fatalf("themes: %d %s", code, h.Get("Location"))
	}
	_, _, settings := request(t, private, "GET", "/settings?saved=appearance", nil, "")
	appearance := strings.SplitN(strings.SplitN(string(settings), `id="appearance"`, 2)[1], "</section>", 2)[0]
	toast := strings.SplitN(strings.SplitN(string(settings), `<div class="toasts" id="toasts">`, 2)[1], "</div>\n</div>", 2)[0]
	if !strings.Contains(toast, "Cairn is on for your library. Shared pages use Minimal.") || strings.Contains(appearance, "notice") {
		t.Error("theme confirmation is not the page's toast")
	}
}

func TestManageExplainsSharingSetup(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	if status, _, body := request(t, srv, "POST", "/api/pages", page("Bulb comparison"), token); status != 201 {
		t.Fatal(status, string(body))
	}
	_, _, body := request(t, srv, "GET", "/manage/report", nil, "")
	for _, want := range []string{"CAIRN_PUBLIC_BASE_URL", "CAIRN_PUBLIC_LISTEN", `href="https://github.com/rowesk/cairn/blob/main/docs/sharing.md"`, `aria-checked="false" aria-labelledby="sharing-title" aria-describedby="sharing-state" disabled`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("not-configured state lacks %q", want)
		}
	}
	code, _, out := authorForm(t, srv, "/manage/report", url.Values{"action": {"share"}}, srv.URL)
	if code != 503 || !strings.Contains(out, "Public links aren’t set up on this server") {
		t.Errorf("share without setup: %d", code)
	}
	// Plain HTTP public addresses cannot carry password cookies.
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "http://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	plain := httptest.NewServer(app)
	defer plain.Close()
	request(t, plain, "POST", "/api/pages", page("Bulb comparison"), token)
	ownerPost(t, plain, "report", url.Values{"action": {"share"}})
	_, _, body = request(t, plain, "GET", "/manage/report", nil, "")
	if !strings.Contains(string(body), "Passwords need a public address that starts with https://.") || !strings.Contains(string(body), `autocomplete="new-password" autocapitalize="none" spellcheck="false" aria-describedby="password-help" disabled`) {
		t.Errorf("plain HTTP password state:\n%s", body)
	}
}

func TestPasswordGateSaysWhoseItIsAndWhatWentWrong(t *testing.T) {
	private, public := startShared(t)
	in := page("Medical results nobody should see")
	in["agent"] = "Codex"
	if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatal(status, string(body))
	}
	for _, v := range []url.Values{{"action": {"share"}}, {"action": {"password"}, "password": {"right password"}}} {
		if code := ownerPost(t, private, "report", v); code != 303 {
			t.Fatal(code)
		}
	}
	check := func(status int, body string, wants ...string) {
		t.Helper()
		if !themed(body) || !strings.Contains(body, `<body class="visitor`) {
			t.Errorf("gate is not the visitor page:\n%s", body)
		}
		if leaks := visitorLeaks(body); len(leaks) > 0 || strings.Contains(body, "Medical results") {
			t.Errorf("gate leaks %v", leaks)
		}
		for _, want := range append(wants, "A page from Codex", "Unlock page", `autofocus`, `data-reveal="gate-password"`) {
			if !strings.Contains(body, want) {
				t.Errorf("gate %d lacks %q", status, want)
			}
		}
		if strings.Contains(body, "report") {
			t.Error("gate still says report")
		}
	}
	res, body := fetch(t, public, "GET", "/report", nil)
	if res.StatusCode != 401 || strings.Contains(markup(string(body)), "aria-invalid") {
		t.Fatalf("empty gate: %d", res.StatusCode)
	}
	check(401, string(body))
	code, _, out := postForm(t, public, "/report", url.Values{"password": {"wrong password"}}, public.URL)
	if code != 401 {
		t.Fatal(code)
	}
	check(code, out, `aria-invalid="true" aria-describedby="gate-password-error"`, `id="gate-password-error" role="alert"`, "That password didn’t match. Check it and try again.")
	// One wrong password costs nothing; the second starts a pause and says
	// how long, and a try inside it is refused with the time left (p9).
	code, h, out := postForm(t, public, "/report", url.Values{"password": {"another guess"}}, public.URL)
	if code != 401 || h.Get("Retry-After") != "1" {
		t.Fatalf("second wrong password: %d %s", code, h.Get("Retry-After"))
	}
	check(code, out, `data-wait="1"`, "That password didn’t match. You can try again in 1 second.")
	code, h, out = postForm(t, public, "/report", url.Values{"password": {"a third guess"}}, public.URL)
	if code != 429 || h.Get("Retry-After") != "1" {
		t.Fatalf("lockout: %d %s", code, h.Get("Retry-After"))
	}
	check(code, out, `aria-invalid="true"`, `data-wait="1"`, "Wait 1 second, then try again.")
	// A post to a page that no longer needs a password just opens it.
	ownerPost(t, private, "report", url.Values{"action": {"remove_password"}})
	code, h, _ = postForm(t, public, "/report", url.Values{"password": {"anything"}}, public.URL)
	if code != 303 || h.Get("Location") != "/report" {
		t.Errorf("unprotected post: %d %s", code, h.Get("Location"))
	}
}

// Notices are toasts: one region per page, fixed to the window's edge, with
// one persistent live region whose text changes. The toast itself is not a
// fresh role="status" element, which screen readers announce unreliably.
func TestNoticesShowInOnePersistentRegion(t *testing.T) {
	private, _ := startShared(t)
	for _, slug := range []string{"first", "second"} {
		in := page("Page " + slug)
		in["slug"] = slug
		request(t, private, "POST", "/api/pages", in, token)
	}
	for _, path := range []string{"/", "/settings", "/import", "/new", "/manage/first", "/nope"} {
		_, _, body := request(t, private, "GET", path, nil, "")
		if strings.Count(string(body), `id="toast-live" role="status" aria-live="polite" aria-atomic="true"></p>`) != 1 || strings.Count(string(body), `<div class="toasts" id="toasts">`) != 1 {
			t.Errorf("%s lacks its one empty live region", path)
		}
	}
	_, _, body := request(t, private, "GET", "/?kindle=sent&page=first", nil, "")
	region := strings.SplitN(strings.SplitN(string(body), `<div class="toasts" id="toasts">`, 2)[1], "</main>", 2)[0]
	if !strings.Contains(region, `<div class="toast is-success" data-toast>`) || !strings.Contains(region, "Sent to your Kindle.") || strings.Contains(string(body), "row-notice") || strings.Contains(string(body), "list-notice") {
		t.Error("Kindle notice is not a toast")
	}
	if strings.Contains(region, `class="toast is-success" role=`) {
		t.Error("the toast is its own live region")
	}
	// A failure says where the fix is: the server's mail account, not a setting.
	_, _, body = request(t, private, "GET", "/?kindle=failed&page=first", nil, "")
	if !strings.Contains(string(body), `<div class="toast is-error" data-toast>`) || !strings.Contains(string(body), `href="https://github.com/rowesk/cairn/blob/main/docs/troubleshooting.md#send-to-kindle">Mail troubleshooting</a>`) || strings.Contains(string(body), `href="/settings#kindle">Kindle settings`) {
		t.Error("failed send points at the wrong fix")
	}
	// Each kind has its own icon, so colour is never the only signal.
	for kind, icon := range map[string]string{"sent": `<path d="M20 6 9 17l-5-5"/>`, "failed": `<line x1="12" x2="12" y1="8" y2="12"/>`, "expired": `<path d="M12 16v-4"/>`} {
		_, _, body := request(t, private, "GET", "/first?kindle="+kind, nil, "")
		parts := strings.SplitN(string(body), `data-toast>`, 2)
		if len(parts) < 2 || !strings.Contains(strings.SplitN(parts[1], "toast-body", 2)[0], icon) {
			t.Errorf("reader %s toast lacks its icon", kind)
		}
	}
	// Saving in the editor opens the reader with a confirmation.
	_, _, body = request(t, private, "GET", "/first?saved=1", nil, "")
	if !strings.Contains(string(body), `<p class="toast-text">Saved.</p>`) {
		t.Error("the reader does not confirm a save")
	}
}
