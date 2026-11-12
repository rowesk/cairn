package cairn_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

// fetch sends one request with explicit headers. Setting Accept-Encoding
// stops the Go client from decompressing, so tests see the bytes on the wire.
func fetch(t *testing.T, srv *httptest.Server, method, path string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func gunzip(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var assetURL = regexp.MustCompile(`/_ui/[a-z0-9.-]+\.(?:css|woff2)`)

func startShared(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	private := httptest.NewServer(app)
	public := httptest.NewServer(app.PublicHandler())
	t.Cleanup(func() { private.Close(); public.Close(); app.Close() })
	return private, public
}

func TestPagesUseOneHashedStylesheetAndStrictStylePolicy(t *testing.T) {
	private, public := startShared(t)
	in := page("Shared report")
	if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	if code, _, _ := authorForm(t, private, "/manage/report", map[string][]string{"action": {"share"}}, private.URL); code != 303 {
		t.Fatal("share", code)
	}
	var stylesheet string
	check := func(srv *httptest.Server, path string, status int) {
		t.Helper()
		res, body := fetch(t, srv, "GET", path, nil)
		html := string(body)
		if res.StatusCode != status {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		links := regexp.MustCompile(`<link rel="stylesheet" href="(/_ui/cairn\.[0-9a-f]{12}\.css)">`).FindAllStringSubmatch(html, -1)
		if len(links) != 1 {
			t.Fatalf("%s: want one hashed stylesheet link, got %d", path, len(links))
		}
		if stylesheet == "" {
			stylesheet = links[0][1]
		} else if links[0][1] != stylesheet {
			t.Fatalf("%s links a different stylesheet", path)
		}
		if strings.Contains(html, "<style") || strings.Contains(html, " style=") || strings.Contains(html, "data:font") {
			t.Fatalf("%s still carries inline styles or fonts", path)
		}
		policy := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(policy, "style-src 'self'; font-src 'self'") || strings.Contains(policy, "'unsafe-inline'") || strings.Contains(policy, "font-src data:") {
			t.Fatalf("%s: style policy not strict: %s", path, policy)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: pages must stay uncached", path)
		}
		if !strings.Contains(html, `<a class="skip-link" href="#main">`) || !strings.Contains(html, `<main id="main"`) {
			t.Fatalf("%s: missing skip link or main landmark", path)
		}
	}
	for _, path := range []string{"/", "/settings", "/new", "/import", "/manage/report", "/manage/report/delete", "/report"} {
		check(private, path, 200)
	}
	check(public, "/report", 200)
	// The password form is the visitor shell: brand, no way into the library.
	if code, _, _ := authorForm(t, private, "/manage/report", map[string][]string{"action": {"password"}, "password": {"secret-words"}}, private.URL); code != 303 {
		t.Fatal("password", code)
	}
	check(public, "/report", 401)
	_, body := fetch(t, public, "GET", "/report", nil)
	if !strings.Contains(string(body), `<span class="brand">`) || strings.Contains(string(body), `href="/"`) || strings.Contains(string(body), "back-link") || strings.Contains(string(body), "topbar-settings") {
		t.Fatal("password form links into the library")
	}
	// Owner pages that used to be bare forms now share the top bar.
	for _, path := range []string{"/import", "/manage/report", "/manage/report/delete"} {
		_, body := fetch(t, private, "GET", path, nil)
		if !strings.Contains(string(body), `<header class="topbar">`) || !strings.Contains(string(body), `<a class="back-link" href="/">`) || !strings.Contains(string(body), `<a class="topbar-settings" href="/settings">`) {
			t.Fatalf("%s: missing shared top bar", path)
		}
	}
}

func TestInterfaceAssetsAreImmutableOnBothListeners(t *testing.T) {
	private, public := startShared(t)
	_, page := fetch(t, private, "GET", "/settings", nil)
	_, sheet := fetch(t, private, "GET", assetURL.FindString(string(page)), nil)
	assets := assetURL.FindAllString(string(page), -1)
	// The stylesheet names its fonts relative to itself: 400, 500 and 600.
	fonts := regexp.MustCompile(`url\((dm-sans-[0-9]+\.[0-9a-f]{12}\.woff2)\) format\('woff2'\)`).FindAllStringSubmatch(string(sheet), -1)
	if len(fonts) != 3 {
		t.Fatalf("expected three font faces, found %v", fonts)
	}
	for _, font := range fonts {
		assets = append(assets, "/_ui/"+font[1])
	}
	for _, srv := range []*httptest.Server{private, public} {
		for _, path := range assets {
			res, body := fetch(t, srv, "GET", path, nil)
			if res.StatusCode != 200 || len(body) == 0 {
				t.Fatalf("%s: %d", path, res.StatusCode)
			}
			if res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatalf("%s: cache %q", path, res.Header.Get("Cache-Control"))
			}
			if res.Header.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%s: missing asset policy", path)
			}
			want := "text/css; charset=utf-8"
			if strings.HasSuffix(path, ".woff2") {
				want = "font/woff2"
				if !bytes.HasPrefix(body, []byte("wOF2")) {
					t.Fatalf("%s is not WOFF2", path)
				}
			}
			if res.Header.Get("Content-Type") != want {
				t.Fatalf("%s: type %q", path, res.Header.Get("Content-Type"))
			}
		}
		// Only published names exist; nothing else under /_ui/ is served.
		for _, path := range []string{"/_ui/", "/_ui/cairn.css", "/_ui/tokens.css", "/_ui/fonts/dm-sans-400.woff2", "/_ui/../settings", "/_ui/dm-sans-700.woff2"} {
			if res, _ := fetch(t, srv, "GET", path, nil); res.StatusCode != 404 {
				t.Fatalf("%s: %d", path, res.StatusCode)
			}
		}
		if res, _ := fetch(t, srv, "POST", assets[0], nil); res.StatusCode != 404 {
			t.Fatal("asset accepted a POST")
		}
	}
	// Stylesheet is identical on both listeners and carries no private data.
	_, a := fetch(t, private, "GET", assets[0], nil)
	_, b := fetch(t, public, "GET", assets[0], nil)
	if !bytes.Equal(a, b) {
		t.Fatal("listeners serve different stylesheets")
	}
}

func TestPublicListenerExposesOnlyTheNewAssets(t *testing.T) {
	_, public := startShared(t)
	for _, path := range []string{"/", "/settings", "/new", "/import", "/manage/x", "/api", "/api/pages", "/_ui", "/_uix/cairn.css", "/_content/x", "/x"} {
		if res, _ := fetch(t, public, "GET", path, nil); res.StatusCode != 404 {
			t.Fatalf("public %s: %d", path, res.StatusCode)
		}
	}
}

func TestTextResponsesAreCompressed(t *testing.T) {
	private, public := startShared(t)
	in := page("Report")
	in["markdown"] = "# Report\n\n```mermaid\ngraph TD; A-->B\n```"
	delete(in, "html")
	if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	gz := map[string]string{"Accept-Encoding": "gzip"}
	_, page := fetch(t, private, "GET", "/", nil)
	css := assetURL.FindString(string(page))
	for _, tc := range []struct{ path, kind string }{
		{"/", "text/html"},
		{"/report", "text/html"},
		{"/_content/report", "text/html"},
		{"/api", "application/json"},
		{css, "text/css"},
		{"/_runtime/boot.js", "text/javascript"},
	} {
		res, body := fetch(t, private, "GET", tc.path, gz)
		if res.Header.Get("Content-Encoding") != "gzip" || !strings.HasPrefix(res.Header.Get("Content-Type"), tc.kind) || !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
			t.Fatalf("%s: encoding %q type %q vary %q", tc.path, res.Header.Get("Content-Encoding"), res.Header.Get("Content-Type"), res.Header.Get("Vary"))
		}
		plain := gunzip(t, body)
		res2, identity := fetch(t, private, "GET", tc.path, map[string]string{"Accept-Encoding": "identity"})
		if res2.Header.Get("Content-Encoding") != "" {
			t.Fatalf("%s: compressed without gzip in Accept-Encoding", tc.path)
		}
		if tc.path != "/" && tc.path != "/report" && !bytes.Equal(plain, identity) {
			t.Fatalf("%s: compressed body differs from identity body", tc.path)
		}
		if len(body) >= len(identity) {
			t.Fatalf("%s: gzip did not shrink the response (%d >= %d)", tc.path, len(body), len(identity))
		}
	}
	// q=0 refuses gzip; HEAD and images pass through untouched.
	if res, _ := fetch(t, private, "GET", "/", map[string]string{"Accept-Encoding": "gzip;q=0"}); res.Header.Get("Content-Encoding") != "" {
		t.Fatal("ignored gzip;q=0")
	}
	if res, body := fetch(t, private, "HEAD", "/", gz); res.Header.Get("Content-Encoding") != "" || len(body) != 0 {
		t.Fatal("HEAD response was compressed")
	}
	res, _ := fetch(t, private, "GET", "/", gz)
	if res.Header.Get("Content-Length") != "" {
		t.Fatal("compressed response kept the identity length")
	}
	// The public listener compresses its reader and password form too.
	if code, _, _ := authorForm(t, private, "/manage/report", map[string][]string{"action": {"share"}}, private.URL); code != 303 {
		t.Fatal("share", code)
	}
	for _, path := range []string{"/report", "/_content/report"} {
		if res, body := fetch(t, public, "GET", path, gz); res.Header.Get("Content-Encoding") != "gzip" || !bytes.Contains(gunzip(t, body), []byte("<")) {
			t.Fatalf("public %s not compressed", path)
		}
	}
}

func TestRuntimeIsVersionedDeferredAndPrecompressed(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	markdown := page("Diagram")
	delete(markdown, "html")
	markdown["slug"] = "diagram"
	markdown["markdown"] = "# Diagram\n\n```mermaid\ngraph TD; A-->B\n```"
	custom := page("Chart")
	custom["slug"] = "chart"
	custom["capabilities"] = []string{"echarts"}
	custom["html"] = `<h1>Chart</h1><div id="c"></div><script>echarts.init(document.getElementById("c"))</script>`
	for _, in := range []map[string]any{markdown, custom} {
		if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
			t.Fatalf("%d %s", status, body)
		}
	}
	tag := regexp.MustCompile(`<script src="(/_runtime/(?:mermaid|echarts|boot)\.js\?v=[0-9a-f]{12})"( defer)?( data-mermaid="/_runtime/mermaid\.js\?v=[0-9a-f]{12}")?></script>`)
	_, _, body := request(t, srv, "GET", "/_content/diagram", nil, "")
	// Mermaid is not loaded with the page: boot.js fetches it when a diagram comes near the screen.
	tags := tag.FindAllStringSubmatch(string(body), -1)
	if len(tags) != 1 || tags[0][2] != " defer" || !strings.Contains(tags[0][1], "boot.js") || tags[0][3] == "" {
		t.Fatalf("Markdown report should load a versioned, deferred runtime that fetches Mermaid when needed: %v", tags)
	}
	_, _, body = request(t, srv, "GET", "/_content/chart", nil, "")
	if tags := tag.FindAllStringSubmatch(string(body), -1); len(tags) != 2 || tags[0][2] != "" {
		t.Fatalf("a report with its own scripts must keep blocking runtime tags: %v", tags)
	}
	versioned := strings.TrimSuffix(strings.TrimPrefix(tags[0][3], ` data-mermaid="`), `"`)
	res, data := fetch(t, srv, "GET", versioned, map[string]string{"Accept-Encoding": "gzip"})
	if res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("versioned runtime headers: %v", res.Header)
	}
	full := gunzip(t, data)
	if len(full) < 1<<20 || len(data) > len(full)/2 {
		t.Fatalf("mermaid should be served pre-compressed: %d of %d", len(data), len(full))
	}
	// Stored pages and old links still reach the plain path, which revalidates.
	res, plain := fetch(t, srv, "GET", "/_runtime/mermaid.js", map[string]string{"Accept-Encoding": "identity"})
	if res.Header.Get("Cache-Control") != "public, max-age=0, must-revalidate" || !bytes.Equal(plain, full) {
		t.Fatal("unversioned runtime changed")
	}
	if res, _ := fetch(t, srv, "GET", "/_runtime/mermaid.js", map[string]string{"Accept-Encoding": "identity", "If-None-Match": res.Header.Get("ETag")}); res.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation: %d", res.StatusCode)
	}
	if res, _ := fetch(t, srv, "GET", strings.Replace(versioned, "?v=", "?v=0", 1), nil); res.Header.Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatal("a stale version must not be cached as immutable")
	}
}
