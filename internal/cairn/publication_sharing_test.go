package cairn_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

func publicPublishingServers(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	private := httptest.NewServer(app)
	public := httptest.NewServer(app.PublicHandler())
	t.Cleanup(func() {
		private.Close()
		public.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	return private, public
}

func TestPublicationSharingOptIn(t *testing.T) {
	for _, mode := range []string{"omitted", "false", "true"} {
		t.Run(mode, func(t *testing.T) {
			private, public := publicPublishingServers(t)
			in := page("Report")
			in["request_summary"] = "PRIVATE_SUMMARY"
			in["request_source"] = "PRIVATE_SOURCE"
			in["html"] = `<h1>Report body</h1><img src="?asset=bulb.png">`
			in["assets"] = map[string]string{"bulb.png": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="}
			if mode != "omitted" {
				in["share_enabled"] = mode == "true"
			}
			code, headers, body := request(t, private, "POST", "/api/pages", in, token)
			if code != 201 {
				t.Fatalf("create: %d %s", code, body)
			}
			var result map[string]string
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if result["id"] == "" || result["url"] != "/report" || headers.Get("Location") != "/report" {
				t.Fatalf("response: %s", body)
			}
			want := 404
			if mode == "true" {
				want = 200
				if result["public_url"] != "https://reports.example.com/report" {
					t.Fatalf("public URL: %s", body)
				}
			} else if _, present := result["public_url"]; present {
				t.Fatalf("private response has public_url: %s", body)
			}
			for _, path := range []string{"/report", "/_content/report", "/_content/report?asset=bulb.png", "/_assets/report/bulb.png"} {
				code, _, body = request(t, public, "GET", path, nil, "")
				if code != want {
					t.Errorf("%s: %d want %d", path, code, want)
				}
				for _, secret := range []string{"PRIVATE:", "PRIVATE_SUMMARY", "PRIVATE_SOURCE", "Manage page"} {
					if strings.Contains(string(body), secret) {
						t.Errorf("%s leaked %s", path, secret)
					}
				}
			}
			if code, _, _ := request(t, private, "GET", "/report", nil, ""); code != 200 {
				t.Fatal("private URL unavailable", code)
			}
			if mode == "true" {
				for _, path := range []string{"/", "/api", "/api/pages", "/manage/report", "/settings"} {
					if code, _, _ := request(t, public, "GET", path, nil, token); code != 404 {
						t.Errorf("public management route %s: %d", path, code)
					}
				}
				if code := ownerPost(t, private, "report", url.Values{"action": {"unshare"}}); code != 303 {
					t.Fatal(code)
				}
				if code, _, _ := request(t, public, "GET", "/report", nil, ""); code != 404 {
					t.Fatal("owner cannot revoke", code)
				}
			}
		})
	}
}

func TestPublicationSharingUnavailableIsAtomic(t *testing.T) {
	dir := t.TempDir()
	private, stop := start(t, dir)
	defer stop()
	in := page("Must not save")
	in["share_enabled"] = true
	code, _, body := request(t, private, "POST", "/api/pages", in, token)
	var failure apiErr
	if err := json.Unmarshal(body, &failure); err != nil {
		t.Fatal(err)
	}
	if code != 503 || failure.Code != "public_sharing_unavailable" || !failure.Retryable {
		t.Fatalf("%d %s", code, body)
	}
	if code, _, _ := request(t, private, "GET", "/report", nil, ""); code != 404 {
		t.Fatal("failed opt-in saved page", code)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "objects"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed opt-in wrote objects: %v %v", entries, err)
	}
	in["share_enabled"] = false
	if code, _, body := request(t, private, "POST", "/api/pages", in, token); code != 201 {
		t.Fatalf("private creation without public config: %d %s", code, body)
	}
}

func TestPublicationSharingFieldValidation(t *testing.T) {
	private, public := publicPublishingServers(t)
	for _, value := range []any{nil, "true", 1, []bool{true}, map[string]bool{"enabled": true}} {
		in := page("Invalid")
		in["share_enabled"] = value
		code, _, body := request(t, private, "POST", "/api/pages", in, token)
		var failure apiErr
		json.Unmarshal(body, &failure)
		if code != 400 || failure.Code != "validation_failed" || failure.Field != "share_enabled" || failure.Retryable {
			t.Errorf("%v: %d %s", value, code, body)
		}
	}
	for _, field := range []string{"public_slug", "password_hash", "password", "share_revision", "Share", "File", "Source"} {
		in := page("Invalid")
		in["share_enabled"] = true
		in[field] = "injected"
		code, _, body := request(t, private, "POST", "/api/pages", in, token)
		var failure apiErr
		json.Unmarshal(body, &failure)
		if code != 400 || failure.Code != "invalid_json" {
			t.Errorf("%s: %d %s", field, code, body)
		}
	}
	if code, _, _ := request(t, public, "GET", "/report", nil, ""); code != 404 {
		t.Fatal("invalid input published", code)
	}
}

func TestReportReplacementPreservesOwnerSharingControls(t *testing.T) {
	for _, mode := range []string{"private", "shared", "password", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			private, public := publicPublishingServers(t)
			if code, _, body := request(t, private, "POST", "/api/pages", page("Original"), token); code != 201 {
				t.Fatalf("%d %s", code, body)
			}
			if mode != "private" {
				if code := ownerPost(t, private, "report", url.Values{"action": {"share"}, "public_slug": {"legacy-alias"}}); code != 303 {
					t.Fatal(code)
				}
			}
			if mode == "password" || mode == "revoked" {
				if code := ownerPost(t, private, "report", url.Values{"action": {"password"}, "password": {"keep-secret"}}); code != 303 {
					t.Fatal(code)
				}
			}
			var grant *http.Cookie
			if mode == "password" {
				req, _ := http.NewRequest("POST", public.URL+"/legacy-alias", strings.NewReader(url.Values{"password": {"keep-secret"}}.Encode()))
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
					t.Fatal("original password failed", res.StatusCode)
				}
				grant = res.Cookies()[0]
			}
			if mode == "revoked" {
				if code := ownerPost(t, private, "report", url.Values{"action": {"unshare"}}); code != 303 {
					t.Fatal(code)
				}
			}
			in := page("Replacement")
			in["html"] = "<h1>Updated body</h1>"
			for _, value := range []any{false, true, nil} {
				in["share_enabled"] = value
				code, _, body := request(t, private, "PUT", "/api/pages/report", in, token)
				var failure apiErr
				json.Unmarshal(body, &failure)
				if code != 400 || failure.Code != "validation_failed" || failure.Field != "share_enabled" {
					t.Errorf("replacement %v: %d %s", value, code, body)
				}
			}
			_, _, body := request(t, private, "GET", "/_content/report", nil, "")
			if strings.Contains(string(body), "Updated body") {
				t.Fatal("rejected replacement changed content")
			}
			delete(in, "share_enabled")
			if code, _, body := request(t, private, "PUT", "/api/pages/report", in, token); code != 200 {
				t.Fatalf("replace: %d %s", code, body)
			}
			if grant != nil {
				req, _ := http.NewRequest("GET", public.URL+"/_content/legacy-alias", nil)
				req.AddCookie(grant)
				res, err := public.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatal("replacement invalidated existing password grant", res.StatusCode)
				}
			}
			for _, route := range []string{"report", "legacy-alias"} {
				code, _, body := request(t, public, "GET", "/_content/"+route, nil, "")
				want := 404
				if mode == "shared" {
					want = 200
				}
				if mode == "password" {
					want = 401
				}
				if code != want {
					t.Errorf("%s %s: %d want %d", mode, route, code, want)
				}
				if mode == "shared" && !strings.Contains(string(body), "Updated body") {
					t.Fatal("shared content not updated")
				}
			}
			if mode == "revoked" {
				if code := ownerPost(t, private, "report", url.Values{"action": {"share"}, "public_slug": {"legacy-alias"}}); code != 303 {
					t.Fatal(code)
				}
				if code, _, _ := request(t, public, "GET", "/_content/legacy-alias", nil, ""); code != 401 {
					t.Fatal("replacement lost saved password", code)
				}
			}
		})
	}
}

func TestPublicCreationGeneratedLinkSurvivesRestart(t *testing.T) {
	cfg := cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com/"}
	app, err := cairn.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	private := httptest.NewServer(app)
	in := page("Generated public report")
	delete(in, "slug")
	in["share_enabled"] = true
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", private.URL+"/api/pages", strings.NewReader(string(data)))
	req.Host = "untrusted.example"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Host", "also-untrusted.example")
	res, err := private.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	err = json.NewDecoder(res.Body).Decode(&result)
	res.Body.Close()
	private.Close()
	if closeErr := app.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || res.StatusCode != 201 || len(result["url"]) != 4 || result["public_url"] != "https://reports.example.com"+result["url"] {
		t.Fatalf("generated public URL: %d %v %v", res.StatusCode, result, err)
	}
	app, err = cairn.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	public := httptest.NewServer(app.PublicHandler())
	defer public.Close()
	if code, _, body := request(t, public, "GET", result["url"], nil, ""); code != 200 || !strings.Contains(string(body), "Generated public report") {
		t.Fatalf("restarted share: %d %s", code, body)
	}
}

func TestAgentKeyCanOptIntoPublicCreationButNotChangeSharing(t *testing.T) {
	private, public := publicPublishingServers(t)
	createKey := func(name string) string {
		t.Helper()
		code, _, body := authorForm(t, private, "/settings", url.Values{"action": {"key_create"}, "name": {name}}, private.URL)
		var result struct{ Key string }
		if err := json.Unmarshal([]byte(body), &result); err != nil || code != 200 || result.Key == "" {
			t.Fatalf("key: %d %s %v", code, body, err)
		}
		return result.Key
	}
	key := createKey("Publisher")
	other := createKey("Other")
	in := page("Agent public report")
	in["share_enabled"] = true
	if code, _, body := request(t, private, "POST", "/api/pages", in, key); code != 201 {
		t.Fatalf("public create: %d %s", code, body)
	}
	_, _, body := request(t, public, "GET", "/report", nil, "")
	if !strings.Contains(string(body), "Prepared by Publisher") || strings.Contains(string(body), "Prepared by ExampleAgent") {
		t.Fatal("key byline not bound")
	}
	delete(in, "share_enabled")
	in["html"] = "<h1>Updated by publisher</h1>"
	if code, _, _ := request(t, private, "PUT", "/api/pages/report", in, other); code != 403 {
		t.Fatal("other agent replaced public report", code)
	}
	if code, _, body := request(t, private, "PUT", "/api/pages/report", in, key); code != 200 {
		t.Fatalf("own replacement: %d %s", code, body)
	}
	if code, _, body := request(t, public, "GET", "/_content/report", nil, ""); code != 200 || !strings.Contains(string(body), "Updated by publisher") {
		t.Fatalf("public replacement: %d %s", code, body)
	}
}
