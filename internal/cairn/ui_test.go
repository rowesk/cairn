package cairn_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
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
	a := strings.LastIndex(html, "<script>") + len("<script>")
	b := strings.LastIndex(html, "</script>")
	sum := sha256.Sum256([]byte(html[a:b]))
	hash := base64.StdEncoding.EncodeToString(sum[:])
	if !strings.Contains(headers.Get("Content-Security-Policy"), "'sha256-"+hash+"'") {
		t.Fatal("preview script blocked by CSP")
	}
	if !strings.Contains(html, `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"`) || strings.Contains(html, "allow-same-origin") {
		t.Fatal("preview sandbox changed")
	}
	_, _, body = request(t, srv, "GET", "/report", nil, "")
	if strings.Contains(string(body), "<dialog") || !strings.Contains(string(body), `src="/_content/report"`) {
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
	if !strings.Contains(string(body), "Find my closest dentist.") {
		t.Fatal("summary search failed")
	}
	in["request_summary"] = strings.Repeat("word ", 41)
	if status, _, _ := request(t, srv, "PUT", "/api/pages/report", in, token); status != 400 {
		t.Fatal("word limit not enforced")
	}
}
