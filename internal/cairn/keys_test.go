package cairn_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentKeyLifecycleAndIsolation(t *testing.T) {
	dir := t.TempDir()
	srv, stop := start(t, dir)
	keyAction := func(action, name string, want int) string {
		t.Helper()
		code, h, body := authorForm(t, srv, "/settings", url.Values{"action": {action}, "name": {name}}, srv.URL)
		if code != want {
			t.Fatalf("%s status %d: %s", action, code, body)
		}
		if want != 200 {
			return ""
		}
		if h.Get("Cache-Control") != "no-store" {
			t.Fatal("key response cacheable")
		}
		var result struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal([]byte(body), &result); err != nil {
			t.Fatal(err)
		}
		return result.Key
	}
	if code, _, _ := authorForm(t, srv, "/settings", url.Values{"action": {"key_create"}, "name": {"ExampleAgent"}}, "https://other.example"); code != 403 {
		t.Fatal("CSRF", code)
	}
	first := keyAction("key_create", "ExampleAgent", 200)
	keyAction("key_create", "ExampleAgent", 409)
	publish := func(method, path, key, slug string, want int) {
		t.Helper()
		code, _, body := request(t, srv, method, path, map[string]any{"slug": slug, "title": "Key test", "agent": "Impersonated", "markdown": "A private page", "original_request": "Private context"}, key)
		if code != want {
			t.Fatalf("%s %s: %d %s", method, path, code, body)
		}
	}
	publish("POST", "/api/pages", first, "agent-one", 201)
	_, _, body := request(t, srv, "GET", "/agent-one", nil, "")
	if !strings.Contains(string(body), "Prepared by ExampleAgent") {
		t.Fatal("byline not bound")
	}
	other := keyAction("key_create", "Other agent", 200)
	publish("PUT", "/api/pages/agent-one", other, "", 403)
	publish("PUT", "/api/pages/agent-one", first, "", 200)
	_, _, body = request(t, srv, "GET", "/settings", nil, "")
	if strings.Contains(string(body), first) || !strings.Contains(string(body), `data-key-used="20`) {
		t.Fatal("secret exposed or last-used absent")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cairn.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), first) {
		t.Fatal("plaintext key stored")
	}
	stop()
	srv, stop = start(t, dir)
	defer stop()
	publish("PUT", "/api/pages/agent-one", first, "", 200)
	second := keyAction("key_rotate", "ExampleAgent", 200)
	if first == second {
		t.Fatal("rotation reused key")
	}
	publish("PUT", "/api/pages/agent-one", first, "", 401)
	publish("PUT", "/api/pages/agent-one", second, "", 200)
	keyAction("key_revoke", "ExampleAgent", 200)
	publish("PUT", "/api/pages/agent-one", second, "", 401)
	keyAction("key_rotate", "ExampleAgent", 409)
	third := keyAction("key_create", "ExampleAgent", 200)
	publish("PUT", "/api/pages/agent-one", third, "", 200)
	publish("POST", "/api/pages", token, "legacy-key", 201)
}
