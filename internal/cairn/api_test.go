package cairn_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

// The publishing API: every endpoint, every error with its field, scope,
// idempotency, warnings, health and request logs.

const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="

type apiApp struct {
	app             *cairn.Server
	private, public *httptest.Server
	logs            *syncBuffer
	dir             string
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startAPI(t *testing.T, private string) *apiApp {
	t.Helper()
	logs := &syncBuffer{}
	dir := t.TempDir()
	app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: token, PublicBaseURL: "https://reports.example.com", PrivateBaseURL: private, Version: "v9.9.9-test", Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	a := &apiApp{app: app, private: httptest.NewServer(app), public: httptest.NewServer(app.PublicHandler()), logs: logs, dir: dir}
	t.Cleanup(func() { a.private.Close(); a.public.Close(); app.Close() })
	return a
}

// call sends one request. body is sent as is when it is a string, as JSON
// otherwise. The reply is decoded into a generic value.
func call(t *testing.T, srv *httptest.Server, method, path string, body any, headers map[string]string) (int, http.Header, map[string]any, string) {
	t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return res.StatusCode, res.Header, decoded, string(raw)
}

func bearer(key string) map[string]string { return map[string]string{"Authorization": "Bearer " + key} }

func newKey(t *testing.T, srv *httptest.Server, name string) string {
	t.Helper()
	code, _, body := authorForm(t, srv, "/settings", url.Values{"action": {"key_create"}, "name": {name}}, srv.URL)
	if code != 200 {
		t.Fatalf("key for %s: %d %s", name, code, body)
	}
	var result struct{ Key string }
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	return result.Key
}

// problems lists an error reply's errors as "code field".
func problems(body map[string]any) []string {
	var out []string
	list, _ := body["errors"].([]any)
	for _, item := range list {
		e := item.(map[string]any)
		out = append(out, fmt.Sprint(e["code"], " ", e["field"]))
	}
	return out
}

func minimal() map[string]any {
	return map[string]any{"title": "Lamps", "agent": "Researcher", "original_request": "find me a lamp", "markdown": "Three lamps."}
}

func with(base map[string]any, changes map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range changes {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

func TestCreateReportsEveryProblemWithItsField(t *testing.T) {
	a := startAPI(t, "")
	big := base64.StdEncoding.EncodeToString(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 4<<20)...))
	many := map[string]string{}
	for i := 0; i < 33; i++ {
		many[fmt.Sprintf("img-%d.png", i)] = pngBase64
	}
	cases := []struct {
		name   string
		body   any
		status int
		code   string
		want   []string
	}{
		{"markdown without a title", with(minimal(), map[string]any{"title": nil}), 400, "validation_failed", []string{"required title"}},
		{"three problems at once", with(minimal(), map[string]any{"title": "", "original_request": nil, "template": "fancy"}), 400, "validation_failed", []string{"required title", "required original_request", "invalid_value template"}},
		{"agent required with the installation token", with(minimal(), map[string]any{"agent": nil}), 400, "validation_failed", []string{"required agent"}},
		{"neither markdown nor html", with(minimal(), map[string]any{"markdown": nil}), 400, "validation_failed", []string{"required markdown"}},
		{"both markdown and html", with(minimal(), map[string]any{"html": "<p>x</p>"}), 400, "validation_failed", []string{"conflict html"}},
		{"unknown field with a likely meaning", with(minimal(), map[string]any{"titel": "x"}), 400, "validation_failed", []string{"unknown_field titel"}},
		{"sharing needs boolean", with(minimal(), map[string]any{"share_enabled": "true"}), 400, "validation_failed", []string{"wrong_type share_enabled"}},
		{"read-only field", with(minimal(), map[string]any{"id": "abc"}), 400, "validation_failed", []string{"read_only id"}},
		{"wrong type", with(minimal(), map[string]any{"title": 5}), 400, "validation_failed", []string{"wrong_type title"}},
		{"bad base64", with(minimal(), map[string]any{"assets": map[string]string{"x.png": "not base64!"}}), 400, "validation_failed", []string{"invalid_base64 assets.x.png"}},
		{"asset not an image", with(minimal(), map[string]any{"assets": map[string]string{"x.png": base64.StdEncoding.EncodeToString([]byte("hello"))}}), 400, "validation_failed", []string{"unsupported_image assets.x.png"}},
		{"asset name", with(minimal(), map[string]any{"assets": map[string]string{"../x.png": pngBase64}}), 400, "validation_failed", []string{"invalid_asset_name assets.../x.png"}},
		{"asset too large", with(minimal(), map[string]any{"assets": map[string]string{"big.png": big}}), 400, "validation_failed", []string{"too_large assets.big.png"}},
		{"too many assets", with(minimal(), map[string]any{"assets": many}), 400, "validation_failed", []string{"too_many_items assets"}},
		{"capability", with(minimal(), map[string]any{"capabilities": []string{"mermaid", "d3"}}), 400, "validation_failed", []string{"invalid_value capabilities[1]"}},
		{"reserved slug", with(minimal(), map[string]any{"slug": "API"}), 400, "validation_failed", []string{"reserved_slug slug"}},
		{"slug shape", with(minimal(), map[string]any{"slug": "my page"}), 400, "validation_failed", []string{"invalid_slug slug"}},
		{"title too long", with(minimal(), map[string]any{"title": strings.Repeat("a", 301)}), 400, "validation_failed", []string{"too_long title"}},
		{"summary words", with(minimal(), map[string]any{"request_summary": strings.Repeat("word ", 41)}), 400, "validation_failed", []string{"too_many_words request_summary"}},
		{"source lines", with(minimal(), map[string]any{"request_source": "Claude\nCode"}), 400, "validation_failed", []string{"multiple_lines request_source"}},
		{"chart with other problems", with(minimal(), map[string]any{"title": nil, "markdown": "```echarts\n{oops}\n```"}), 400, "validation_failed", []string{"required title", "invalid_chart markdown"}},
		{"not JSON", `{"title": "x",}`, 400, "invalid_json", []string{"syntax <nil>"}},
		{"not an object", `["title"]`, 400, "invalid_json", []string{"syntax <nil>"}},
		{"two objects", `{} {}`, 400, "invalid_json", []string{"syntax <nil>"}},
		{"empty", ` `, 400, "invalid_json", []string{"syntax <nil>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, header, body, raw := call(t, a.private, "POST", "/api/pages", tc.body, bearer(token))
			if status != tc.status || body["code"] != tc.code || header.Get("Content-Type") != "application/json" {
				t.Fatalf("got %d %v, want %d %s: %s", status, body["code"], tc.status, tc.code, raw)
			}
			if got := problems(body); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("errors %v, want %v: %s", got, tc.want, raw)
			}
			// Older clients read the first problem at the top level.
			first := body["errors"].([]any)[0].(map[string]any)
			if body["message"] != first["message"] || (first["field"] != nil && body["field"] != first["field"]) || body["retryable"] != false {
				t.Fatalf("top level does not repeat the first problem: %s", raw)
			}
		})
	}
	if status, _, body, _ := call(t, a.private, "GET", "/api/pages", nil, bearer(token)); status != 200 || body["total"] != float64(0) {
		t.Fatalf("a refused publication was saved: %v", body)
	}
}

func TestProblemsCarryLimitsActualsAndAllowedValues(t *testing.T) {
	a := startAPI(t, "")
	first := func(body map[string]any) map[string]any { return body["errors"].([]any)[0].(map[string]any) }
	_, _, body, raw := call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"title": strings.Repeat("é", 200)}), bearer(token))
	if e := first(body); e["limit"] != float64(300) || e["actual"] != float64(400) || e["unit"] != "bytes" || !strings.Contains(e["message"].(string), "400 bytes; the limit is 300 bytes") {
		t.Errorf("title limit: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"template": "fancy"}), bearer(token))
	if e := first(body); e["actual"] != "fancy" || fmt.Sprint(e["allowed"]) != "[report comparison visual]" {
		t.Errorf("template allowed: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"request_summary": strings.Repeat("word ", 45)}), bearer(token))
	if e := first(body); e["limit"] != float64(40) || e["actual"] != float64(45) || e["unit"] != "words" {
		t.Errorf("summary words: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"titel": "x"}), bearer(token))
	if e := first(body); !strings.Contains(e["message"].(string), "Did you mean title?") {
		t.Errorf("unknown field suggestion: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"assets": map[string]string{"x.png": base64.StdEncoding.EncodeToString([]byte("hello"))}}), bearer(token))
	if e := first(body); e["actual"] != "text/plain" || fmt.Sprint(e["allowed"]) != "[png jpeg gif webp]" {
		t.Errorf("image type: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "POST", "/api/pages", "{\n  \"title\": \"x\",\n}", bearer(token))
	if e := first(body); e["line"] != float64(3) || e["column"] != float64(1) || !strings.Contains(body["message"].(string), "line 3, column 1") {
		t.Errorf("JSON position: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"markdown": "Intro.\n\n```echarts\n{\"series\": [1,]}\n```"}), bearer(token))
	if e := first(body); e["line"] != float64(4) || e["block"] != float64(1) || e["code"] != "invalid_chart" {
		t.Errorf("chart position: %s", raw)
	}
	// Base64 without padding, with line breaks or as a data URL is accepted.
	for i, encoded := range []string{strings.TrimRight(pngBase64, "="), pngBase64[:40] + "\n" + pngBase64[40:], "data:image/png;base64," + pngBase64} {
		in := with(minimal(), map[string]any{"slug": fmt.Sprintf("img-%d", i), "assets": map[string]string{"x.png": encoded}})
		if status, _, _, raw := call(t, a.private, "POST", "/api/pages", in, bearer(token)); status != 201 {
			t.Errorf("base64 form %d: %s", i, raw)
		}
	}
	big := strings.Repeat("a", 17<<20)
	status, _, body, raw := call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"markdown": big}), bearer(token))
	if e := first(body); status != 413 || body["code"] != "payload_too_large" || e["limit"] != float64(16<<20) {
		t.Errorf("payload limit: %d %.300s", status, raw)
	}
	if status, _, body, _ := call(t, a.private, "POST", "/api/pages", with(minimal(), nil), map[string]string{"Authorization": "Bearer " + token, "Content-Type": "text/plain"}); status != 415 || body["code"] != "unsupported_media_type" {
		t.Errorf("media type: %d %v", status, body)
	}
}

func TestAuthFailuresSayWhichProblem(t *testing.T) {
	a := startAPI(t, "")
	key := newKey(t, a.private, "Researcher")
	if code, _, body := authorForm(t, a.private, "/settings", url.Values{"action": {"key_revoke"}, "name": {"Researcher"}}, a.private.URL); code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	cases := []struct {
		name, header, code string
	}{
		{"missing header", "", "authentication_required"},
		{"not bearer", "Basic dXNlcjpwYXNz", "authentication_required"},
		{"empty bearer", "Bearer ", "authentication_required"},
		{"wrong key", "Bearer cairn_wrongwrongwrongwrongwrongwrongwrongwrongwro", "invalid_credential"},
		{"wrong token", "Bearer not-the-installation-token-at-all-really", "invalid_credential"},
		{"revoked key", "Bearer " + key, "invalid_credential"},
	}
	for _, tc := range cases {
		for _, route := range []struct{ method, path string }{{"POST", "/api/pages"}, {"GET", "/api/pages"}, {"GET", "/api/pages/x"}, {"PATCH", "/api/pages/x"}} {
			headers := map[string]string{}
			if tc.header != "" {
				headers["Authorization"] = tc.header
			}
			status, header, body, raw := call(t, a.private, route.method, route.path, "{}", headers)
			if status != 401 || body["code"] != tc.code || header.Get("WWW-Authenticate") == "" {
				t.Errorf("%s %s %s: %d %s", tc.name, route.method, route.path, status, raw)
			}
		}
	}
	logs := a.logs.String()
	if strings.Count(logs, `msg="auth failure"`) != len(cases)*4 || !strings.Contains(logs, "reason=missing") || !strings.Contains(logs, "reason=invalid") || !strings.Contains(logs, "reason=malformed") {
		t.Errorf("auth failures not logged one line each:\n%s", logs)
	}
	if strings.Contains(logs, key) || strings.Contains(logs, "wrongwrong") || strings.Contains(logs, "dXNlcjpwYXNz") {
		t.Error("a credential reached the log")
	}
}

func TestCreateAnswersWithThePageAndWarnings(t *testing.T) {
	a := startAPI(t, "https://cairn.example.ts.net")
	key := newKey(t, a.private, "Researcher")
	in := map[string]any{"title": "Lamps", "agent": "Someone else", "original_request": "PRIVATE-REQUEST", "markdown": "Intro <span onclick=\"x()\">here</span>.\n\n<script>alert(1)</script>\n\n<iframe src=\"https://x\"></iframe>"}
	status, header, body, raw := call(t, a.private, "POST", "/api/pages", in, map[string]string{"Authorization": "Bearer " + key, "Host": "elsewhere.example.ts.net"})
	if status != 201 {
		t.Fatalf("create: %d %s", status, raw)
	}
	slug := body["slug"].(string)
	if body["url"] != "/"+slug || header.Get("Location") != "/"+slug || body["private_url"] != "https://cairn.example.ts.net/"+slug || body["id"] == "" || body["created_at"] == "" || body["updated_at"] != body["created_at"] || body["agent"] != "Researcher" || body["format"] != "markdown" || body["template"] != "report" || body["editable"] != true {
		t.Fatalf("response: %s", raw)
	}
	codes := map[string]map[string]any{}
	for _, w := range body["warnings"].([]any) {
		codes[w.(map[string]any)["code"].(string)] = w.(map[string]any)
	}
	if fmt.Sprint(codes["dropped_html"]["tags"]) != "[iframe script]" || fmt.Sprint(codes["dropped_html"]["attributes"]) != "[onclick]" || codes["agent_ignored"] == nil || codes["private_url_unset"] != nil {
		t.Fatalf("warnings: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "POST", "/api/pages", map[string]any{"title": "Custom", "original_request": "r", "html": "<p>Mine</p>", "template": "visual"}, bearer(key))
	if fmt.Sprint(body["warnings"]) != "[map[code:template_ignored message:template applies only to Markdown, so Cairn kept the HTML as sent.]]" || body["format"] != "html" || body["template"] != nil {
		t.Errorf("template warning: %s", raw)
	}
	// Without a private base URL Cairn says so instead of guessing from Host.
	b := startAPI(t, "")
	_, _, body, raw = call(t, b.private, "POST", "/api/pages", minimal(), map[string]string{"Authorization": "Bearer " + token, "Host": "elsewhere.example.ts.net"})
	if body["private_url"] != nil || !strings.Contains(raw, `"code":"private_url_unset"`) || strings.Contains(raw, "elsewhere") {
		t.Errorf("unset private URL: %s", raw)
	}
}

func TestListSearchAndScope(t *testing.T) {
	a := startAPI(t, "https://cairn.example.ts.net")
	mine, theirs := newKey(t, a.private, "Researcher"), newKey(t, a.private, "Planner")
	for i, title := range []string{"Walnut worktops compared", "Oak flooring", "Kitchen taps"} {
		in := map[string]any{"title": title, "original_request": "PRIVATE-" + title, "markdown": fmt.Sprintf("Page %d about the kitchen. The quartzite option is dearer.", i)}
		if status, _, _, raw := call(t, a.private, "POST", "/api/pages", in, bearer(mine)); status != 201 {
			t.Fatal(raw)
		}
	}
	if status, _, _, raw := call(t, a.private, "POST", "/api/pages", map[string]any{"title": "Holiday plan", "original_request": "PRIVATE-PLAN", "markdown": "Kitchen-free week."}, bearer(theirs)); status != 201 {
		t.Fatal(raw)
	}
	status, _, body, raw := call(t, a.private, "GET", "/api/pages", nil, bearer(token))
	if status != 200 || body["total"] != float64(4) || len(body["pages"].([]any)) != 4 || body["has_more"] != false || body["next_cursor"] != nil {
		t.Fatalf("installation token list: %s", raw)
	}
	status, _, body, raw = call(t, a.private, "GET", "/api/pages", nil, bearer(mine))
	if status != 200 || body["total"] != float64(3) || strings.Contains(raw, "Holiday plan") || strings.Contains(raw, "PRIVATE") {
		t.Fatalf("agent key list must hold only its own pages and no requests: %s", raw)
	}
	if status, _, body, _ := call(t, a.private, "GET", "/api/pages?agent=Planner", nil, bearer(mine)); status != 403 || body["field"] != "agent" {
		t.Errorf("another agent's list: %d %v", status, body)
	}
	status, _, body, raw = call(t, a.private, "GET", "/api/pages?q=quartz", nil, bearer(mine))
	if status != 200 || body["total"] != float64(3) {
		t.Fatalf("search: %s", raw)
	}
	match := body["pages"].([]any)[0].(map[string]any)["match"].(map[string]any)
	spans := match["highlights"].([]any)
	if match["in"] != "page" || len(spans) != 1 {
		t.Fatalf("match: %v", match)
	}
	span := spans[0].([]any)
	if text := []rune(match["text"].(string)); string(text[int(span[0].(float64)):int(span[1].(float64))]) != "quartzite" {
		t.Errorf("highlight does not cover the word: %v", match)
	}
	_, _, body, raw = call(t, a.private, "GET", "/api/pages?q=walnut", nil, bearer(mine))
	if body["total"] != float64(1) || body["pages"].([]any)[0].(map[string]any)["match"].(map[string]any)["in"] != "title" {
		t.Errorf("title match: %s", raw)
	}
	if _, _, body, _ := call(t, a.private, "GET", "/api/pages?q=holiday", nil, bearer(mine)); body["total"] != float64(0) {
		t.Errorf("search reached another agent's page: %v", body)
	}
	// Paging.
	_, _, body, raw = call(t, a.private, "GET", "/api/pages?limit=2", nil, bearer(mine))
	if len(body["pages"].([]any)) != 2 || body["has_more"] != true || body["next_cursor"] == nil {
		t.Fatalf("first page: %s", raw)
	}
	_, _, next, raw := call(t, a.private, "GET", "/api/pages?limit=2&cursor="+url.QueryEscape(body["next_cursor"].(string)), nil, bearer(mine))
	if len(next["pages"].([]any)) != 1 || next["has_more"] != false || next["next_cursor"] != nil {
		t.Fatalf("second page: %s", raw)
	}
	item := next["pages"].([]any)[0].(map[string]any)
	if item["private_url"] != "https://cairn.example.ts.net"+item["url"].(string) || item["excerpt"] == "" || item["updated_at"] == "" {
		t.Errorf("list item: %v", item)
	}
	for query, want := range map[string]string{"limit=0": "invalid_value limit", "limit=101": "invalid_value limit", "cursor=zzz": "invalid_value cursor", "archived=maybe": "invalid_value archived", "search=x": "unknown_field search"} {
		status, _, body, raw := call(t, a.private, "GET", "/api/pages?"+query, nil, bearer(mine))
		if status != 400 || strings.Join(problems(body), "|") != want {
			t.Errorf("%s: %d %s", query, status, raw)
		}
	}
	if _, _, body, _ := call(t, a.private, "GET", "/api/pages?search=x", nil, bearer(mine)); !strings.Contains(fmt.Sprint(body["message"]), "Did you mean q?") {
		t.Errorf("parameter suggestion: %v", body["message"])
	}
}

func TestReadReturnsSourceWithinScope(t *testing.T) {
	a := startAPI(t, "")
	mine, theirs := newKey(t, a.private, "Researcher"), newKey(t, a.private, "Planner")
	markdown := "Intro.\n\n![Lamp](?asset=lamp.png)\n\n```mermaid\ngraph LR\nA-->B\n```"
	call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "lamps", "title": "Lamps", "original_request": "PRIVATE-MINE", "markdown": markdown, "assets": map[string]string{"lamp.png": pngBase64}}, bearer(mine))
	call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "custom", "title": "Custom", "original_request": "r", "html": "<!doctype html><title>x</title><pre><code class=\"language-mermaid\">graph LR\nA-->B</code></pre>", "capabilities": []string{"mermaid"}}, bearer(mine))
	call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "plan", "title": "Plan", "original_request": "PRIVATE-THEIRS", "markdown": "Their page."}, bearer(theirs))
	status, _, body, raw := call(t, a.private, "GET", "/api/pages/lamps", nil, bearer(mine))
	source := body["source"].(map[string]any)
	if status != 200 || source["text"] != markdown || source["format"] != "markdown" || body["original_request"] != "PRIVATE-MINE" || fmt.Sprint(body["assets"]) != "[lamp.png]" || body["editable"] != true {
		t.Fatalf("read markdown: %s", raw)
	}
	_, _, body, raw = call(t, a.private, "GET", "/api/pages/custom", nil, bearer(mine))
	text, _ := body["source"].(map[string]any)["text"].(string)
	if body["format"] != "html" || !strings.Contains(text, "language-mermaid") || strings.Contains(text, "/_runtime/") {
		t.Fatalf("read html: %s", raw)
	}
	// What GET returns can be sent back unchanged.
	if status, _, _, raw := call(t, a.private, "PUT", "/api/pages/custom", map[string]any{"title": "Custom", "original_request": "r", "html": text, "capabilities": []string{"mermaid"}}, bearer(mine)); status != 200 {
		t.Fatalf("round trip: %s", raw)
	}
	_, _, again, _ := call(t, a.private, "GET", "/api/pages/custom", nil, bearer(mine))
	if again["source"].(map[string]any)["text"] != text {
		t.Error("html changed on a round trip")
	}
	// Another author's page answers exactly as a missing one does.
	status, _, body, raw = call(t, a.private, "GET", "/api/pages/plan", nil, bearer(mine))
	if status != 404 || body["code"] != "not_found" || raw != strings.ReplaceAll(missingAnswer(t, a, "GET", "nothing-here", mine), "nothing-here", "plan") || strings.Contains(raw, "PRIVATE-THEIRS") || strings.Contains(raw, "Their page") {
		t.Fatalf("another agent's page: %d %s", status, raw)
	}
	if status, _, body, raw := call(t, a.private, "GET", "/api/pages/plan", nil, bearer(token)); status != 200 || body["original_request"] != "PRIVATE-THEIRS" {
		t.Fatalf("installation token reads all: %s", raw)
	}
	for path, want := range map[string]int{"/api/pages/lamps": 200, "/api/pages/plan": 404, "/api/pages/missing": 404, "/api/pages/settings": 404} {
		status, _, _, raw := call(t, a.private, "HEAD", path, nil, bearer(mine))
		if status != want || raw != "" {
			t.Errorf("HEAD %s: %d %q", path, status, raw)
		}
	}
	if status, _, body, _ := call(t, a.private, "GET", "/api/pages/missing", nil, bearer(mine)); status != 404 || body["code"] != "not_found" || body["field"] != "slug" {
		t.Errorf("missing: %d %v", status, body)
	}
	if status, _, _, _ := call(t, a.private, "GET", "/api/pages/a/b", nil, bearer(mine)); status != 404 {
		t.Errorf("nested path: %d", status)
	}
}

func TestPatchArchivesRetitlesAndNeverDeletes(t *testing.T) {
	a := startAPI(t, "")
	mine, theirs := newKey(t, a.private, "Researcher"), newKey(t, a.private, "Planner")
	call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "lamps", "title": "Lamps", "original_request": "r", "markdown": "Body"}, bearer(mine))
	call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "plan", "title": "Plan", "original_request": "r", "markdown": "Body"}, bearer(theirs))
	status, _, body, raw := call(t, a.private, "PATCH", "/api/pages/lamps", map[string]any{"archived": true, "title": "Desk lamps compared"}, bearer(mine))
	if status != 200 || body["archived"] != true || body["title"] != "Desk lamps compared" || body["updated_at"] == "" {
		t.Fatalf("patch: %d %s", status, raw)
	}
	if _, _, body, _ := call(t, a.private, "GET", "/api/pages?archived=true&q=desk", nil, bearer(mine)); body["total"] != float64(1) {
		t.Errorf("archived page not found by its new title: %v", body)
	}
	if _, _, body, _ := call(t, a.private, "GET", "/api/pages", nil, bearer(mine)); body["total"] != float64(0) {
		t.Errorf("archived page still listed: %v", body)
	}
	if status, _, body, _ := call(t, a.private, "PATCH", "/api/pages/lamps", map[string]any{"archived": false}, bearer(mine)); status != 200 || body["archived"] != false {
		t.Errorf("restore: %d %v", status, body)
	}
	for name, tc := range map[string]struct {
		body   any
		status int
		want   string
	}{
		"empty":        {map[string]any{}, 400, "required archived"},
		"wrong type":   {map[string]any{"archived": "yes"}, 400, "wrong_type archived"},
		"unknown":      {map[string]any{"markdown": "x"}, 400, "unknown_field markdown"},
		"empty title":  {map[string]any{"title": " "}, 400, "required title"},
		"sharing":      {map[string]any{"shared": true}, 400, "not_allowed shared"},
		"other author": {map[string]any{"archived": true}, 404, ""},
	} {
		path := "/api/pages/lamps"
		if name == "other author" {
			path = "/api/pages/plan"
		}
		status, _, body, raw := call(t, a.private, "PATCH", path, tc.body, bearer(mine))
		if status != tc.status || strings.Join(problems(body), "|") != tc.want {
			t.Errorf("%s: %d %s", name, status, raw)
		}
	}
	status, header, body, _ := call(t, a.private, "DELETE", "/api/pages/lamps", nil, bearer(mine))
	if status != 405 || body["code"] != "method_not_allowed" || !strings.Contains(body["message"].(string), "PATCH") || header.Get("Allow") != "GET, HEAD, PUT, PATCH" {
		t.Errorf("delete: %d %v", status, body)
	}
	if status, _, _, _ := call(t, a.private, "GET", "/api/pages/lamps", nil, bearer(mine)); status != 200 {
		t.Error("delete removed the page")
	}
}

func TestReplaceKeepsScopeAndSlug(t *testing.T) {
	a := startAPI(t, "")
	mine, theirs := newKey(t, a.private, "Researcher"), newKey(t, a.private, "Planner")
	call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "lamps", "title": "Lamps", "original_request": "r", "request_summary": "Find a lamp.", "markdown": "One"}, bearer(mine))
	status, _, body, raw := call(t, a.private, "PUT", "/api/pages/lamps", map[string]any{"title": "Lamps", "original_request": "r", "markdown": "Two"}, bearer(mine))
	if status != 200 || body["request_summary"] != "Find a lamp." || body["updated_at"] == "" {
		t.Fatalf("replace: %s", raw)
	}
	if status, _, body, _ := call(t, a.private, "PUT", "/api/pages/lamps", map[string]any{"title": "Lamps", "original_request": "r", "markdown": "Three"}, bearer(theirs)); status != 404 || body["code"] != "not_found" || body["message"] != `No page has the slug "lamps". To publish a new page, POST to /api/pages.` {
		t.Errorf("another agent replaced the page: %d %v", status, body)
	}
	if status, _, body, _ := call(t, a.private, "PUT", "/api/pages/lamps", map[string]any{"slug": "other", "title": "Lamps", "original_request": "r", "markdown": "Three"}, bearer(mine)); status != 400 || strings.Join(problems(body), "") != "invalid_value slug" {
		t.Errorf("slug change: %d %v", status, body)
	}
	if status, _, body, _ := call(t, a.private, "PUT", "/api/pages/nope", map[string]any{"title": "Lamps", "original_request": "r", "markdown": "Three"}, bearer(mine)); status != 404 || body["field"] != "slug" {
		t.Errorf("missing: %d %v", status, body)
	}
}

func TestSlugsAreUniqueIgnoringCase(t *testing.T) {
	a := startAPI(t, "")
	if status, _, _, raw := call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"slug": "lamp-report"}), bearer(token)); status != 201 {
		t.Fatal(raw)
	}
	status, _, body, raw := call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"slug": "Lamp-Report"}), bearer(token))
	// The answer never names the page that holds the slug: it may be another
	// author's.
	if status != 409 || body["code"] != "slug_conflict" || body["field"] != "slug" || strings.Contains(raw, `"lamp-report"`) || !strings.Contains(body["message"].(string), "differ only in letter case") || strings.Join(problems(body), "") != "slug_taken slug" {
		t.Fatalf("case variant: %d %s", status, raw)
	}
	if status, _, body, _ := call(t, a.private, "POST", "/api/pages", with(minimal(), map[string]any{"slug": "lamp-report"}), bearer(token)); status != 409 || !strings.Contains(body["message"].(string), "Leave slug out") {
		t.Errorf("exact: %d %v", status, body)
	}
}

func TestIdempotencyKeysReplayOncePerCredential(t *testing.T) {
	a := startAPI(t, "")
	key := newKey(t, a.private, "Researcher")
	in := map[string]any{"title": "Lamps", "original_request": "r", "markdown": "Body"}
	headers := map[string]string{"Authorization": "Bearer " + key, "Idempotency-Key": "lamps-1"}
	status, first, body, raw := call(t, a.private, "POST", "/api/pages", in, headers)
	if status != 201 || first.Get("Idempotent-Replayed") != "" {
		t.Fatal(raw)
	}
	status, again, replay, replayRaw := call(t, a.private, "POST", "/api/pages", in, headers)
	if status != 201 || again.Get("Idempotent-Replayed") != "true" || replayRaw != raw || again.Get("Location") != "/"+body["slug"].(string) {
		t.Fatalf("retry: %d %s", status, replayRaw)
	}
	if replay["slug"] != body["slug"] {
		t.Fatal("retry made another page")
	}
	if _, _, list, _ := call(t, a.private, "GET", "/api/pages", nil, bearer(key)); list["total"] != float64(1) {
		t.Fatalf("pages after retry: %v", list["total"])
	}
	// The same key for a different request is refused.
	status, _, body, _ = call(t, a.private, "POST", "/api/pages", with(in, map[string]any{"title": "Other"}), headers)
	if status != 422 || body["code"] != "idempotency_key_reused" {
		t.Errorf("reuse: %d %v", status, body)
	}
	// Keys belong to one credential: the installation token's lamps-1 is new.
	status, _, body, raw = call(t, a.private, "POST", "/api/pages", with(in, map[string]any{"agent": "Other"}), map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "lamps-1"})
	if status != 201 || body["agent"] != "Other" {
		t.Errorf("scope: %d %s", status, raw)
	}
	// A refused request keeps no result, so the corrected one can use its key.
	retryHeaders := map[string]string{"Authorization": "Bearer " + key, "Idempotency-Key": "fix-me"}
	if status, _, _, _ := call(t, a.private, "POST", "/api/pages", with(in, map[string]any{"title": nil}), retryHeaders); status != 400 {
		t.Error("invalid request accepted")
	}
	if status, _, _, raw := call(t, a.private, "POST", "/api/pages", in, retryHeaders); status != 201 {
		t.Errorf("corrected request: %s", raw)
	}
	for _, bad := range []string{"has space", strings.Repeat("k", 256)} {
		status, _, body, _ := call(t, a.private, "POST", "/api/pages", in, map[string]string{"Authorization": "Bearer " + key, "Idempotency-Key": bad})
		if status != 400 || strings.Join(problems(body), "") != "invalid_value Idempotency-Key" {
			t.Errorf("key %q: %d %v", bad, status, body)
		}
	}
	// Replacements and updates replay too.
	slug := replay["slug"].(string)
	patch := map[string]string{"Authorization": "Bearer " + key, "Idempotency-Key": "archive-1"}
	call(t, a.private, "PATCH", "/api/pages/"+slug, map[string]any{"archived": true}, patch)
	call(t, a.private, "PATCH", "/api/pages/"+slug, map[string]any{"archived": false}, bearer(key))
	// A replay never runs again; it answers with the page as it is now,
	// which the unkeyed PATCH has restored.
	if status, header, body, _ := call(t, a.private, "PATCH", "/api/pages/"+slug, map[string]any{"archived": true}, patch); status != 200 || header.Get("Idempotent-Replayed") != "true" || body["archived"] != false {
		t.Errorf("patch replay: %d %v", status, body)
	}
}

func TestBrowsersCannotUseTheAPI(t *testing.T) {
	a := startAPI(t, "")
	for _, headers := range []map[string]string{{"Origin": "https://evil.example"}, {"Sec-Fetch-Site": "cross-site"}, {"Sec-Fetch-Site": "same-origin"}, {"Origin": a.private.URL}} {
		headers["Authorization"] = "Bearer " + token
		for _, route := range []struct{ method, path string }{{"GET", "/api/pages"}, {"POST", "/api/pages"}, {"GET", "/api/pages/x"}, {"PUT", "/api/pages/x"}, {"PATCH", "/api/pages/x"}} {
			status, _, body, _ := call(t, a.private, route.method, route.path, "{}", headers)
			if status != 403 || body["code"] != "browser_origin_forbidden" {
				t.Errorf("%v %s %s: %d %v", headers, route.method, route.path, status, body)
			}
		}
	}
	// A page the owner types into the address bar is not a cross-site request.
	if status, _, _, _ := call(t, a.private, "GET", "/api/pages", nil, map[string]string{"Authorization": "Bearer " + token, "Sec-Fetch-Site": "none"}); status != 200 {
		t.Errorf("typed navigation: %d", status)
	}
}

func TestHealthOnBothListeners(t *testing.T) {
	a := startAPI(t, "")
	status, header, body, raw := call(t, a.private, "GET", "/healthz", nil, nil)
	if status != 200 || body["status"] != "ok" || body["version"] != "v9.9.9-test" || body["database"] != "ok" || body["storage"] != "ok" || header.Get("Content-Type") != "application/json" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("private: %d %s", status, raw)
	}
	status, _, _, raw = call(t, a.public, "GET", "/healthz", nil, nil)
	if status != 200 || strings.TrimSpace(raw) != `{"status":"ok"}` {
		t.Fatalf("public: %d %s", status, raw)
	}
	if status, _, _, raw := call(t, a.public, "HEAD", "/healthz", nil, nil); status != 200 || raw != "" {
		t.Errorf("public HEAD: %d", status)
	}
	if status, _, _, _ := call(t, a.private, "POST", "/healthz", "{}", nil); status != 405 {
		t.Errorf("private POST: %d", status)
	}
	if status, _, _, _ := call(t, a.public, "POST", "/healthz", "{}", nil); status != 404 {
		t.Errorf("public POST: %d", status)
	}
	objects := filepath.Join(a.dir, "objects")
	if err := os.Chmod(objects, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(objects, 0700)
	status, _, body, raw = call(t, a.private, "GET", "/healthz", nil, nil)
	if status != 503 || body["status"] != "unavailable" || body["storage"] != "unavailable" || body["database"] != "ok" {
		t.Fatalf("unwritable storage: %d %s", status, raw)
	}
	if status, _, _, raw := call(t, a.public, "GET", "/healthz", nil, nil); status != 503 || strings.TrimSpace(raw) != `{"status":"unavailable"}` {
		t.Errorf("public unwritable: %d %s", status, raw)
	}
	if !strings.Contains(a.logs.String(), `level=ERROR msg="server error" listener=private method=GET path=/healthz status=503`) {
		t.Errorf("health failure not logged:\n%s", a.logs.String())
	}
}

func TestRequestLogsNameEventsAndNeverSecrets(t *testing.T) {
	a := startAPI(t, "")
	key := newKey(t, a.private, "Researcher")
	secretRequest, secretTitle, secretBody := "PRIVATE-REQUEST-WORDS", "Secret title words", "Secret body words"
	in := map[string]any{"slug": "lamps", "title": secretTitle, "original_request": secretRequest, "markdown": secretBody}
	call(t, a.private, "POST", "/api/pages", in, bearer(key))
	call(t, a.private, "PUT", "/api/pages/lamps", in, bearer(key))
	call(t, a.private, "PATCH", "/api/pages/lamps", map[string]any{"archived": true}, bearer(key))
	call(t, a.private, "POST", "/api/pages", in, bearer("cairn_not-a-real-key-not-a-real-key-not-a-real"))
	// A storage failure is a server error.
	moved := a.dir + "-offline"
	if err := os.Rename(a.dir, moved); err != nil {
		t.Fatal(err)
	}
	status, _, _, _ := call(t, a.private, "PUT", "/api/pages/lamps", with(in, map[string]any{"markdown": "Another"}), bearer(key))
	if err := os.Rename(moved, a.dir); err != nil {
		t.Fatal(err)
	}
	if status != 500 {
		t.Fatalf("storage failure: %d", status)
	}
	logs := a.logs.String()
	for _, want := range []string{
		`level=INFO msg=publish listener=private method=POST path=/api/pages status=201`,
		`slug=lamps agent=Researcher credential=agent_key format=markdown`,
		`level=INFO msg=replace listener=private method=PUT path=/api/pages/lamps status=200`,
		`level=INFO msg=update listener=private method=PATCH path=/api/pages/lamps status=200`,
		`level=WARN msg="auth failure" listener=private method=POST path=/api/pages status=401`,
		`level=ERROR msg="server error" listener=private method=PUT path=/api/pages/lamps status=500`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
	if lines := strings.Count(logs, "\n"); lines != 5 {
		t.Errorf("want one line per event, got %d:\n%s", lines, logs)
	}
	for _, secret := range []string{key, token, secretRequest, secretTitle, secretBody, "not-a-real-key", "Bearer"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log holds %q", secret)
		}
	}
}

func TestPrivateBaseURLMustBeAnOrigin(t *testing.T) {
	for _, bad := range []string{"cairn.example.ts.net", "https://cairn.example.ts.net/library", "ftp://x", "https://user@x.example"} {
		if app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PrivateBaseURL: bad}); err == nil {
			app.Close()
			t.Errorf("accepted %q", bad)
		}
	}
}

// missingAnswer is the exact body a key gets for a slug no page has.
func missingAnswer(t *testing.T, a *apiApp, method, slug, key string) string {
	t.Helper()
	var body any
	if method == "PUT" {
		body = map[string]any{"title": "x", "original_request": "r", "markdown": "x"}
	} else if method == "PATCH" {
		body = map[string]any{"archived": true}
	}
	status, _, _, raw := call(t, a.private, method, "/api/pages/"+slug, body, bearer(key))
	if status != 404 {
		t.Fatalf("%s missing page: %d %s", method, status, raw)
	}
	return raw
}

// An agent key cannot tell another author's page from a missing one, by
// status or by body, on any method, and a taken slug is never echoed back
// in its stored spelling.
func TestOtherAuthorsPagesLookMissing(t *testing.T) {
	a := startAPI(t, "")
	mine, theirs := newKey(t, a.private, "Researcher"), newKey(t, a.private, "Planner")
	if status, _, _, raw := call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "Their-Plan", "title": "Plan", "original_request": "r", "markdown": "x"}, bearer(theirs)); status != 201 {
		t.Fatal(raw)
	}
	for _, method := range []string{"GET", "HEAD", "PUT", "PATCH"} {
		var body any
		switch method {
		case "PUT":
			body = map[string]any{"title": "x", "original_request": "r", "markdown": "x"}
		case "PATCH":
			body = map[string]any{"archived": true}
		}
		status, header, _, raw := call(t, a.private, method, "/api/pages/Their-Plan", body, bearer(mine))
		missStatus, missHeader, _, _ := call(t, a.private, method, "/api/pages/Not-A-Plan", body, bearer(mine))
		want := strings.ReplaceAll(missingAnswer(t, a, method, "Not-A-Plan", mine), "Not-A-Plan", "Their-Plan")
		if status != 404 || missStatus != 404 || raw != want || header.Get("Content-Type") != missHeader.Get("Content-Type") {
			t.Errorf("%s: %d %q, missing page gives %d %q", method, status, raw, missStatus, want)
		}
	}
	status, _, _, raw := call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "their-plan", "title": "x", "original_request": "r", "markdown": "x"}, bearer(mine))
	if status != 409 || strings.Contains(raw, "Their-Plan") {
		t.Errorf("case variant echoed the stored slug: %d %s", status, raw)
	}
	if _, _, body, _ := call(t, a.private, "GET", "/api/pages/Their-Plan", nil, bearer(theirs)); body["title"] != "Plan" {
		t.Error("the author lost their own page")
	}
}
