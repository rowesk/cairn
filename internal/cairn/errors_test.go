package cairn_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type apiErr struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Field     string `json:"field"`
	Retryable bool   `json:"retryable"`
}

func apiFailure(t *testing.T, srv string, client *http.Client, method, path, contentType, body, credential, origin string) (int, http.Header, apiErr) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var failure apiErr
	if err := json.Unmarshal(raw, &failure); err != nil {
		t.Fatalf("%s %s status %d returned non-JSON: %q", method, path, res.StatusCode, raw)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s %s content type %q, want application/json", method, path, ct)
	}
	if failure.Code == "" || failure.Message == "" {
		t.Fatalf("%s %s missing code/message: %q", method, path, raw)
	}
	return res.StatusCode, res.Header, failure
}

func TestAPIErrorContract(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	client := srv.Client()

	cases := []struct {
		name       string
		method     string
		path       string
		ctype      string
		body       string
		credential string
		origin     string
		status     int
		code       string
	}{
		{"missing credential", "POST", "/api/pages", "application/json", `{}`, "", "", 401, "authentication_required"},
		{"wrong credential", "POST", "/api/pages", "application/json", `{}`, "wrong", "", 401, "authentication_required"},
		{"browser origin", "POST", "/api/pages", "application/json", `{}`, token, "https://untrusted.example", 403, "browser_origin_forbidden"},
		{"media type", "POST", "/api/pages", "text/plain", `{}`, token, "", 415, "unsupported_media_type"},
		{"malformed JSON", "POST", "/api/pages", "application/json", `{oops`, token, "", 400, "invalid_json"},
		{"trailing JSON", "POST", "/api/pages", "application/json", `{} {}`, token, "", 400, "invalid_json"},
		{"validation", "POST", "/api/pages", "application/json", `{"title":"","agent":"ExampleAgent","original_request":"x","html":"<p>x</p>"}`, token, "", 400, "validation_failed"},
		{"unknown field", "POST", "/api/pages", "application/json", `{"title":"x","agent":"ExampleAgent","original_request":"x","html":"<p>x</p>","share_enabled":true}`, token, "", 400, "invalid_json"},
		{"unknown route", "GET", "/api/nope", "", "", token, "", 404, "not_found"},
		{"wrong method", "DELETE", "/api/pages", "", "", token, "", 405, "method_not_allowed"},
	}
	for _, tc := range cases {
		if status, _, failure := apiFailure(t, srv.URL, client, tc.method, tc.path, tc.ctype, tc.body, tc.credential, tc.origin); status != tc.status || failure.Code != tc.code {
			t.Errorf("%s: got %d %q, want %d %q", tc.name, status, failure.Code, tc.status, tc.code)
		}
	}

	status, _, body := request(t, srv, "POST", "/api/pages", page("Conflict page"), token)
	if status != 201 {
		t.Fatalf("setup: %d %s", status, body)
	}
	if status, _, failure := apiFailure(t, srv.URL, client, "POST", "/api/pages", "application/json", `{"title":"Conflict page","agent":"ExampleAgent","original_request":"x","html":"<p>x</p>","slug":"report"}`, token, ""); status != 409 || failure.Code != "slug_conflict" {
		t.Errorf("collision: %d %q", status, failure.Code)
	}
	if status, _, failure := apiFailure(t, srv.URL, client, "PUT", "/api/pages/missing", "application/json", `{"title":"x","agent":"ExampleAgent","original_request":"x","html":"<p>x</p>"}`, token, ""); status != 404 || failure.Code != "not_found" {
		t.Errorf("replace missing: %d %q", status, failure.Code)
	}

	// One key cannot replace another author's page.
	first := func(action, name string) string {
		t.Helper()
		code, _, text := authorForm(t, srv, "/settings", url.Values{"action": {action}, "name": {name}}, srv.URL)
		if code != 200 {
			t.Fatalf("key %s: %d %s", action, code, text)
		}
		var result struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatal(err)
		}
		return result.Key
	}
	woodhouse := first("key_create", "ExampleAgent")
	other := first("key_create", "Other agent")
	if status, _, _ := request(t, srv, "POST", "/api/pages", map[string]any{"slug": "owned", "title": "Owned", "agent": "Ignored", "markdown": "Owned page", "original_request": "Owned request"}, woodhouse); status != 201 {
		t.Fatalf("owned create: %d", status)
	}
	if status, _, failure := apiFailure(t, srv.URL, client, "PUT", "/api/pages/owned", "application/json", `{"title":"Owned","agent":"Ignored","markdown":"Hi","original_request":"Owned request"}`, other, ""); status != 403 || failure.Code != "forbidden" {
		t.Errorf("cross-author replace: %d %q", status, failure.Code)
	}

	// Success responses are JSON too.
	success := page("JSON success")
	delete(success, "slug")
	if status, headers, _ := request(t, srv, "POST", "/api/pages", success, token); status != 201 || headers.Get("Content-Type") != "application/json" {
		t.Errorf("success content type: %d %q", status, headers.Get("Content-Type"))
	}
	if status, headers, _ := request(t, srv, "GET", "/api", nil, ""); status != 200 || headers.Get("Content-Type") != "application/json" {
		t.Errorf("index content type: %d %q", status, headers.Get("Content-Type"))
	}
}

func TestOversizedPayloadReturns413(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	big := strings.Repeat("a", (17 << 20))
	payload, err := json.Marshal(map[string]any{"title": "Too big", "agent": "ExampleAgent", "original_request": "x", "html": "<p>" + big + "</p>"})
	if err != nil {
		t.Fatal(err)
	}
	status, _, failure := apiFailure(t, srv.URL, srv.Client(), "POST", "/api/pages", "application/json", string(payload), token, "")
	if status != http.StatusRequestEntityTooLarge || failure.Code != "payload_too_large" {
		t.Fatalf("oversize: %d %q", status, failure.Code)
	}
	if status, _, _ := request(t, srv, "GET", "/too-big", nil, ""); status != 404 {
		t.Error("oversized payload created a page")
	}
}
