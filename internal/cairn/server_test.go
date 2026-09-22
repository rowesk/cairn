package cairn_test

import (
	"bytes"
	"encoding/json"
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
	"time"

	"github.com/rowesk/cairn/internal/cairn"
)

const token = "test-publisher-token-with-at-least-32-characters"

func start(t *testing.T, dir string) (*httptest.Server, func()) {
	t.Helper()
	app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app)
	return srv, func() {
		srv.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}
}
func request(t *testing.T, srv *httptest.Server, method, path string, body any, credential string) (int, http.Header, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, res.Header, b
}
func page(title string) map[string]any {
	return map[string]any{"title": title, "agent": "ExampleAgent", "original_request": "PRIVATE: compare bulbs for my bedroom", "html": "<h1>Bulb report</h1>", "slug": "report"}
}
func TestPublishAndReadPrivatePage(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	status, _, body := request(t, srv, "POST", "/api/pages", page("Bulb comparison"), token)
	if status != http.StatusCreated {
		t.Fatalf("publish: %d %s", status, body)
	}
	var result struct{ ID, URL string }
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || result.URL != "/report" {
		t.Fatalf("bad result: %s", body)
	}
	status, _, body = request(t, srv, "GET", result.URL, nil, "")
	if status != 200 || !strings.Contains(string(body), "Prepared by ExampleAgent") || !strings.Contains(string(body), "PRIVATE:") {
		t.Fatalf("private view: %d %s", status, body)
	}
	status, headers, body := request(t, srv, "GET", "/_content/report", nil, "")
	if status != 200 || !strings.Contains(string(body), "Bulb report") || strings.Contains(string(body), "PRIVATE:") {
		t.Fatalf("report content: %d %s", status, body)
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox;") {
		t.Fatal("direct HTML lacks sandbox")
	}
}

func TestPublishingRequiresCredentialAndRejectsSharing(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	for _, credential := range []string{"", "wrong"} {
		status, _, _ := request(t, srv, "POST", "/api/pages", page("Private"), credential)
		if status != 401 {
			t.Errorf("credential %q got %d, want 401", credential, status)
		}
	}
	in := page("Private")
	in["share_enabled"] = true
	status, _, _ := request(t, srv, "POST", "/api/pages", in, token)
	if status != 400 {
		t.Errorf("sharing field got %d, want 400", status)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/pages", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://untrusted.example")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("browser-origin write got %d, want 403", res.StatusCode)
	}
	status, _, _ = request(t, srv, "GET", "/report", nil, "")
	if status != 404 {
		t.Errorf("rejected publication created a page: %d", status)
	}
}

func TestAssetsAndRoutesPersistAfterRestart(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	in := page("Images")
	delete(in, "slug")
	in["assets"] = map[string]string{"bulb.png": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="}
	status, _, body := request(t, srv, "POST", "/api/pages", in, token)
	if status != 201 {
		close()
		t.Fatalf("publish images: %d %s", status, body)
	}
	var result struct{ ID, URL string }
	json.Unmarshal(body, &result)
	close()
	srv, close = start(t, dir)
	defer close()
	status, _, body = request(t, srv, "GET", result.URL, nil, "")
	if status != 200 || !strings.Contains(string(body), "Images") {
		t.Fatalf("restart: %d %s", status, body)
	}
	status, headers, body := request(t, srv, "GET", "/_assets"+result.URL+"/bulb.png", nil, "")
	if status != 200 || headers.Get("Content-Type") != "image/png" || !bytes.HasPrefix(body, []byte{137, 80, 78, 71}) {
		t.Fatalf("asset: %d %s %q", status, headers, body)
	}
	for _, path := range []string{"/_assets/other/bulb.png", "/_assets" + result.URL + "/../page.html", "/_content" + result.URL + "/page.html", "/objects/" + result.ID + "/page.html"} {
		status, _, _ := request(t, srv, "GET", path, nil, "")
		if status != 404 {
			t.Errorf("unsafe route %s got %d", path, status)
		}
	}
}
func TestInvalidPublicationDoesNotCreatePage(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	for _, slug := range []string{"api", "_content", "healthz", "library", "../secret", "with/slash", "with space", "report.html", ""} {
		in := page("Invalid")
		if slug == "" {
			in["title"] = ""
		} else {
			in["slug"] = slug
		}
		status, _, _ := request(t, srv, "POST", "/api/pages", in, token)
		if status != 400 {
			t.Errorf("invalid %q got %d", slug, status)
		}
	}
	for _, assets := range []map[string]string{{"../secret.png": "aGVsbG8="}, {"secret.html": "PGgxPnNlY3JldDwvaDE+"}, {"bad.png": "not-base64"}} {
		in := page("Invalid asset")
		in["assets"] = assets
		status, _, _ := request(t, srv, "POST", "/api/pages", in, token)
		if status != 400 {
			t.Errorf("invalid asset got %d", status)
		}
	}
}

func TestExplicitReplacementAndFailurePreservePage(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	status, _, body := request(t, srv, "POST", "/api/pages", page("Original"), token)
	if status != 201 {
		close()
		t.Fatalf("create: %d %s", status, body)
	}
	var original struct{ ID string }
	json.Unmarshal(body, &original)
	status, _, _ = request(t, srv, "POST", "/api/pages", page("Accidental overwrite"), token)
	if status != 409 {
		t.Errorf("collision: %d", status)
	}
	in := page("Updated")
	in["html"] = "<h1>Replacement</h1>"
	status, _, body = request(t, srv, "PUT", "/api/pages/report", in, token)
	if status != 200 {
		t.Errorf("replace: %d %s", status, body)
	}
	var updated struct{ ID string }
	json.Unmarshal(body, &updated)
	if original.ID != updated.ID {
		t.Error("replacement changed identity")
	}
	in["assets"] = map[string]string{"../bad.png": "aGVsbG8="}
	status, _, _ = request(t, srv, "PUT", "/api/pages/report", in, token)
	if status != 400 {
		t.Errorf("invalid replace: %d", status)
	}
	close()
	srv, close = start(t, dir)
	defer close()
	status, _, body = request(t, srv, "GET", "/_content/report", nil, "")
	if status != 200 || !strings.HasPrefix(string(body), "<h1>Replacement</h1>\n<script>") {
		t.Fatalf("failed update/restart lost current content: %d %s", status, body)
	}
	status, _, _ = request(t, srv, "PUT", "/api/pages/missing", page("Missing"), token)
	if status != 404 {
		t.Errorf("replace missing: %d", status)
	}
}

func TestStorageFailureKeepsPreviousPage(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	defer close()
	status, _, body := request(t, srv, "POST", "/api/pages", page("Original"), token)
	if status != 201 {
		t.Fatalf("create: %d %s", status, body)
	}
	// Simulate storage disappearing, without modifying or inspecting the database.
	moved := dir + "-offline"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	in := page("Replacement that must fail")
	status, _, _ = request(t, srv, "PUT", "/api/pages/report", in, token)
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	if status != 500 {
		t.Fatalf("storage failure got %d", status)
	}
	status, _, body = request(t, srv, "GET", "/report", nil, "")
	if status != 200 || !strings.Contains(string(body), "Original") || strings.Contains(string(body), "Replacement that must fail") {
		t.Fatalf("lost original: %d %s", status, body)
	}
}

func TestGeneratedPageCanReferenceItsOwnImage(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Generated image report")
	delete(in, "slug")
	in["html"] = `<img src="?asset=bulb.png" alt="Bulb">`
	in["assets"] = map[string]string{"bulb.png": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="}
	status, _, body := request(t, srv, "POST", "/api/pages", in, token)
	if status != 201 {
		t.Fatalf("publish: %d %s", status, body)
	}
	var result struct{ URL string }
	json.Unmarshal(body, &result)
	status, headers, _ := request(t, srv, "GET", "/_content"+result.URL+"?asset=bulb.png", nil, "")
	if status != 200 || headers.Get("Content-Type") != "image/png" {
		t.Fatalf("relative image: %d %s", status, headers)
	}
	status, _, _ = request(t, srv, "GET", "/_content"+result.URL+"?asset=../page.html", nil, "")
	if status != 404 {
		t.Errorf("traversal got %d", status)
	}
}

func TestCleanupFailureIsReportedAndRetried(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	defer close()
	status, _, body := request(t, srv, "POST", "/api/pages", page("Original"), token)
	if status != 201 {
		t.Fatalf("create: %d %s", status, body)
	}
	// Deny cleanup of the old object while leaving room to publish its replacement.
	entries, err := os.ReadDir(filepath.Join(dir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "objects", entries[0].Name())
	if err := os.Chmod(old, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(old, 0700)
	status, _, body = request(t, srv, "PUT", "/api/pages/report", page("Replacement"), token)
	if status != 200 || !strings.Contains(string(body), "cleanup pending") {
		t.Fatalf("cleanup warning: %d %s", status, body)
	}
	status, _, _ = request(t, srv, "PUT", "/api/pages/report", page("Another replacement"), token)
	if status != 503 {
		t.Errorf("unresolved cleanup got %d", status)
	}
	if err := os.Chmod(old, 0700); err != nil {
		t.Fatal(err)
	}
	status, _, body = request(t, srv, "PUT", "/api/pages/report", page("After recovery"), token)
	if status != 200 || strings.Contains(string(body), "warning") {
		t.Fatalf("cleanup recovery: %d %s", status, body)
	}
	status, _, body = request(t, srv, "GET", "/report", nil, "")
	if status != 200 || !strings.Contains(string(body), "After recovery") {
		t.Fatalf("read recovery: %d %s", status, body)
	}
}

func TestMarkdownTemplatesPersistAsHTML(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	for _, layout := range []string{"report", "comparison", "visual"} {
		in := page("Bulb comparison")
		delete(in, "html")
		in["markdown"] = "# Bulbs\n\n| Name | Power |\n|---|---|\n| LED | 6W |\n\n![Bulb](?asset=bulb.png)"
		in["template"] = layout
		in["slug"] = layout + "-page"
		status, _, body := request(t, srv, "POST", "/api/pages", in, token)
		if status != 201 {
			close()
			t.Fatalf("markdown: %d %s", status, body)
		}
	}
	close()
	srv, close = start(t, dir)
	defer close()
	for _, layout := range []string{"report", "comparison", "visual"} {
		status, _, body := request(t, srv, "GET", "/_content/"+layout+"-page", nil, "")
		for _, want := range []string{"<h1>Bulbs</h1>", "table-scroll", "data-template=\"" + layout + "\"", "<style>", "<a href=\"?asset=bulb.png\""} {
			if status != 200 || !strings.Contains(string(body), want) {
				t.Errorf("%s missing %s: %d %s", layout, want, status, body)
			}
		}
		if strings.Contains(string(body), "PRIVATE:") {
			t.Error("private request leaked")
		}
	}
	in := page("Ambiguous")
	in["markdown"] = "also Markdown"
	status, _, _ := request(t, srv, "POST", "/api/pages", in, token)
	if status != 400 {
		t.Errorf("ambiguous formats: %d", status)
	}
}

func TestManualImportUsesPrivatePublishingPath(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	status, _, body := request(t, srv, "GET", "/import", nil, "")
	if status != 200 {
		t.Fatalf("form: %d", status)
	}
	marker := `name="csrf" value="`
	parts := strings.Split(string(body), marker)
	if len(parts) != 2 {
		t.Fatal("missing CSRF field")
	}
	csrf := strings.Split(parts[1], `"`)[0]
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for k, v := range map[string]string{"csrf": csrf, "title": "Imported report", "agent": "Owner", "original_request": "Save this file", "template": "report", "slug": "imported"} {
		form.WriteField(k, v)
	}
	f, _ := form.CreateFormFile("document", "notes.md")
	io.WriteString(f, "# Imported Markdown\n\nA saved file.")
	form.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/import", &buf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Origin", srv.URL)
	client := *srv.Client()
	client.CheckRedirect = func(r *http.Request, v []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != "/imported" {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("import: %d %s", res.StatusCode, b)
	}
	status, _, body = request(t, srv, "GET", "/_content/imported", nil, "")
	if status != 200 || !strings.Contains(string(body), "<h1>Imported Markdown</h1>") {
		t.Fatalf("imported content: %d %s", status, body)
	}
}

func TestSharedRuntimeIsPackagedWithInteractivePage(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Chart")
	in["capabilities"] = []string{"echarts", "mermaid"}
	status, _, body := request(t, srv, "POST", "/api/pages", in, token)
	if status != 201 {
		t.Fatalf("publish: %d %s", status, body)
	}
	status, headers, body := request(t, srv, "GET", "/_content/report", nil, "")
	if status != 200 || !strings.Contains(string(body), "/_runtime/echarts.js") || !strings.Contains(headers.Get("Content-Security-Policy"), "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox;") {
		t.Fatalf("runtime missing: %d %s", status, body)
	}
	status, _, body = request(t, srv, "GET", "/_runtime/echarts.js", nil, "")
	if status != 200 || len(body) < 100000 {
		t.Fatalf("packaged runtime: %d", status)
	}
	in["slug"] = "unknown-runtime"
	in["capabilities"] = []string{"install-whatever"}
	status, _, _ = request(t, srv, "POST", "/api/pages", in, token)
	if status != 400 {
		t.Errorf("unknown capability: %d", status)
	}
}

func ownerPost(t *testing.T, srv *httptest.Server, slug string, values url.Values) int {
	t.Helper()
	_, _, body := request(t, srv, "GET", "/manage/"+slug, nil, "")
	match := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("missing owner form: %s", body)
	}
	values.Set("csrf", string(match[1]))
	req, _ := http.NewRequest("POST", srv.URL+"/manage/"+slug, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	return res.StatusCode
}
func TestLibrarySearchAndPageControls(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	in := page("Bulb comparison")
	in["html"] = "<p>Useful light report</p>"
	status, _, _ := request(t, srv, "POST", "/api/pages", in, token)
	if status != 201 {
		close()
		t.Fatal(status)
	}
	status, _, body := request(t, srv, "GET", "/?q=bedroom&agent=ExampleAgent", nil, "")
	if status != 200 || !strings.Contains(string(body), "Bulb comparison") || !strings.Contains(string(body), "Useful light report") {
		close()
		t.Fatalf("library: %d %s", status, body)
	}
	if status := ownerPost(t, srv, "report", url.Values{"action": {"rename"}, "title": {"Renamed report"}}); status != 303 {
		t.Fatal(status)
	}
	if status := ownerPost(t, srv, "report", url.Values{"action": {"archive"}}); status != 303 {
		t.Fatal(status)
	}
	_, _, body = request(t, srv, "GET", "/", nil, "")
	if strings.Contains(string(body), "Renamed report") {
		t.Error("archived item in main list")
	}
	close()
	srv, close = start(t, dir)
	defer close()
	_, _, body = request(t, srv, "GET", "/?archived=1", nil, "")
	if !strings.Contains(string(body), "Renamed report") {
		t.Error("archived item lost")
	}
	status, _, _ = request(t, srv, "GET", "/report", nil, "")
	if status != 200 {
		t.Error("archive broke link")
	}
	if status := ownerPost(t, srv, "report", url.Values{"action": {"restore"}}); status != 303 {
		t.Fatal(status)
	}
	if status := ownerPost(t, srv, "report/delete", url.Values{"action": {"delete"}}); status != 303 {
		t.Fatal(status)
	}
	status, _, _ = request(t, srv, "GET", "/report", nil, "")
	if status != 404 {
		t.Error("deleted page still served")
	}
}

func TestPublicShareIsSeparateAndRevocable(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	private := httptest.NewServer(app)
	defer private.Close()
	public := httptest.NewServer(app.PublicHandler())
	defer public.Close()
	in := page("Shareable report")
	in["assets"] = map[string]string{"bulb.png": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="}
	in["html"] = `<h1>Public report</h1><img src="/_assets/report/bulb.png">`
	status, _, _ := request(t, private, "POST", "/api/pages", in, token)
	if status != 201 {
		t.Fatal(status)
	}
	for _, path := range []string{"/report", "/", "/api/pages", "/import", "/manage/report", "/_content/report"} {
		status, _, _ = request(t, public, "GET", path, nil, "")
		if status != 404 {
			t.Errorf("private public route %s: %d", path, status)
		}
	}
	if status := ownerPost(t, private, "report", url.Values{"action": {"share"}, "public_slug": {"bulbs"}}); status != 303 {
		t.Fatal(status)
	}
	status, _, body := request(t, public, "GET", "/bulbs", nil, "")
	if status != 200 || !strings.Contains(string(body), "Prepared by ExampleAgent") || strings.Contains(string(body), "PRIVATE:") || strings.Contains(string(body), "Manage page") {
		t.Fatalf("public view: %d %s", status, body)
	}
	status, headers, body := request(t, public, "GET", "/_content/bulbs", nil, "")
	if status != 200 || strings.Contains(string(body), "PRIVATE:") || !strings.Contains(string(body), "/_assets/bulbs/bulb.png") || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("public content: %d %s", status, body)
	}
	status, _, _ = request(t, public, "GET", "/_assets/bulbs/bulb.png", nil, "")
	if status != 200 {
		t.Fatal(status)
	}
	if status := ownerPost(t, private, "report", url.Values{"action": {"unshare"}}); status != 303 {
		t.Fatal(status)
	}
	for _, path := range []string{"/bulbs", "/_content/bulbs", "/_assets/bulbs/bulb.png"} {
		status, _, _ = request(t, public, "GET", path, nil, "")
		if status != 404 {
			t.Errorf("revoked %s: %d", path, status)
		}
	}
}

func TestSharePasswordProtectsAssetsAndInvalidatesGrants(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	private := httptest.NewServer(app)
	defer private.Close()
	public := httptest.NewTLSServer(app.PublicHandler())
	defer public.Close()
	in := page("Password report")
	in["assets"] = map[string]string{"bulb.png": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="}
	request(t, private, "POST", "/api/pages", in, token)
	if status := ownerPost(t, private, "report", url.Values{"action": {"share"}, "public_slug": {"protected"}}); status != 303 {
		t.Fatal(status)
	}
	if status := ownerPost(t, private, "report", url.Values{"action": {"password"}, "password": {"correct-password"}}); status != 303 {
		t.Fatal(status)
	}
	status, _, _ := request(t, public, "GET", "/_assets/protected/bulb.png", nil, "")
	if status != 401 {
		t.Fatalf("unprotected asset: %d", status)
	}
	values := url.Values{"password": {"correct-password"}}
	req, _ := http.NewRequest("POST", public.URL+"/protected", strings.NewReader(values.Encode()))
	req.Header.Set("Origin", public.URL)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *public.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || len(res.Cookies()) != 1 {
		t.Fatalf("unlock: %d", res.StatusCode)
	}
	cookie := res.Cookies()[0]
	fetchAsset := func() int {
		r, _ := http.NewRequest("GET", public.URL+"/_assets/protected/bulb.png", nil)
		r.AddCookie(cookie)
		v, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		v.Body.Close()
		return v.StatusCode
	}
	if got := fetchAsset(); got != 200 {
		t.Fatalf("authorized image: %d", got)
	}
	if status := ownerPost(t, private, "report", url.Values{"action": {"password"}, "password": {"new-password"}}); status != 303 {
		t.Fatal(status)
	}
	if got := fetchAsset(); got != 401 {
		t.Fatalf("old grant survived password change: %d", got)
	}
	if status := ownerPost(t, private, "report", url.Values{"action": {"unshare"}}); status != 303 {
		t.Fatal(status)
	}
	if got := fetchAsset(); got != 404 {
		t.Fatalf("revoked share: %d", got)
	}
}

func TestPasswordShareScopeRestartAndRemoval(t *testing.T) {
	cfg := cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"}
	app, err := cairn.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	private := httptest.NewServer(app)
	public := httptest.NewTLSServer(app.PublicHandler())
	defer func() { private.Close(); public.Close(); app.Close() }()
	for _, slug := range []string{"report", "second"} {
		in := page(slug)
		in["slug"] = slug
		request(t, private, "POST", "/api/pages", in, token)
		for _, v := range []url.Values{{"action": {"share"}, "public_slug": {slug}}, {"action": {"password"}, "password": {"secret"}}} {
			if got := ownerPost(t, private, slug, v); got != 303 {
				t.Fatal(got)
			}
		}
	}
	unlock := func(password string) (int, []*http.Cookie) {
		req, _ := http.NewRequest("POST", public.URL+"/report", strings.NewReader(url.Values{"password": {password}}.Encode()))
		req.Header.Set("Origin", public.URL)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		client := *public.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode, res.Cookies()
	}
	if status, cookies := unlock("wrong"); status != 401 || len(cookies) != 0 {
		t.Fatal(status)
	}
	time.Sleep(1100 * time.Millisecond)
	status, cookies := unlock("secret")
	if status != 303 || len(cookies) != 1 {
		t.Fatal(status)
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly {
		t.Fatal("insecure grant")
	}
	get := func(path string) int {
		req, _ := http.NewRequest("GET", public.URL+path, nil)
		req.AddCookie(cookie)
		res, err := public.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if get("/_content/report") != 200 || get("/_content/second") != 401 {
		t.Fatal("grant scope")
	}
	private.Close()
	public.Close()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	app, err = cairn.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	private = httptest.NewServer(app)
	public = httptest.NewTLSServer(app.PublicHandler())
	if get("/_content/report") != 200 {
		t.Fatal("grant lost on restart")
	}
	if got := ownerPost(t, private, "report", url.Values{"action": {"remove_password"}}); got != 303 {
		t.Fatal(got)
	}
	if status, _, _ := request(t, public, "GET", "/_content/report", nil, ""); status != 200 {
		t.Fatal("password removal")
	}
	if got := ownerPost(t, private, "report", url.Values{"action": {"password"}, "password": {"secret"}}); got != 303 {
		t.Fatal(got)
	}
	if get("/_content/report") != 401 {
		t.Fatal("old grant revived")
	}
}
