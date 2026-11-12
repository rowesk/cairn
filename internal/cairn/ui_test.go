package cairn_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestTideLibraryKeepsDirectLinksAndSandbox(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page(`A report <script>alert(1)</script>`)
	if status, _, _ := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
		t.Fatal(status)
	}
	status, headers, body := request(t, srv, "GET", "/", nil, "")
	html := string(body)
	if status != 200 || !strings.Contains(html, `href="/report" data-preview`) || strings.Contains(html, `<script>alert(1)</script>`) {
		t.Fatal("unsafe or missing direct link")
	}
	// The page's script is a cached file under /_ui/, allowed by script-src 'self', never inline.
	script := regexp.MustCompile(`<script src="(/_ui/library\.[0-9a-f]{12}\.js)"></script>`).FindStringSubmatch(html)
	if script == nil || strings.Contains(html, "<script>") || !strings.Contains(headers.Get("Content-Security-Policy"), "script-src 'self'") || strings.Contains(headers.Get("Content-Security-Policy"), "unsafe-inline") {
		t.Fatal("preview script not served as a file under a strict policy")
	}
	if status, h, js := request(t, srv, "GET", script[1], nil, ""); status != 200 || !strings.Contains(h.Get("Content-Type"), "javascript") || !strings.Contains(string(js), "cairnFollowScheme") {
		t.Fatal("library script missing", status)
	}
	if !strings.Contains(html, `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"`) || strings.Contains(html, "allow-same-origin") {
		t.Fatal("preview sandbox changed")
	}
	_, _, body = request(t, srv, "GET", "/report", nil, "")
	if strings.Contains(string(body), "<dialog") || !strings.Contains(string(body), `src="/_content/report?scheme=auto"`) {
		t.Fatal("direct link must be a full reading page")
	}
	_, _, body = request(t, srv, "GET", "/?shared=1", nil, "")
	if strings.Contains(string(body), `href="/report" data-preview`) {
		t.Fatal("private report in shared filter")
	}
	if status := ownerPost(t, srv, "report", url.Values{"action": {"archive"}}); status != 303 {
		t.Fatal(status)
	}
	_, _, body = request(t, srv, "GET", "/?archived=1", nil, "")
	if !strings.Contains(string(body), `href="/report" data-preview`) {
		t.Fatal("archive link missing")
	}
}

func TestRequestSummaryPublicationAndReplacement(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Report")
	in["request_summary"] = "Find my closest dentist."
	in["request_source"] = "Hermes"
	if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("%d %s", status, body)
	}
	delete(in, "request_summary")
	delete(in, "request_source")
	if status, _, body := request(t, srv, "PUT", "/api/pages/report", in, token); status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	_, _, body := request(t, srv, "GET", "/report", nil, "")
	if !strings.Contains(string(body), "Find my closest dentist.") || !strings.Contains(string(body), "via Hermes") {
		t.Fatal("replacement lost metadata")
	}
	_, _, body = request(t, srv, "GET", "/?q=dentist", nil, "")
	if !strings.Contains(string(body), "Find my closest <mark>dentist</mark>.") {
		t.Fatal("summary search failed")
	}
	in["request_summary"] = strings.Repeat("word ", 41)
	if status, _, _ := request(t, srv, "PUT", "/api/pages/report", in, token); status != 400 {
		t.Fatal("word limit not enforced")
	}
}
