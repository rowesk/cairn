package cairn_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// The private library installs as an app with its own icon; shared pages
// show the icon in the tab but never offer to install themselves.
func TestIconsAndManifest(t *testing.T) {
	private, public, closeApp := openThemed(t, t.TempDir())
	defer closeApp()
	custom := page("Router shortlist")
	custom["slug"] = "router"
	if code, _, _ := request(t, private, "POST", "/api/pages", custom, token); code != 201 {
		t.Fatal(code)
	}
	if code := ownerPost(t, private, "router", url.Values{"action": {"share"}}); code != 303 {
		t.Fatal(code)
	}
	png := []byte("\x89PNG\r\n\x1a\n")
	for _, srv := range []string{"private", "public"} {
		s := private
		if srv == "public" {
			s = public
		}
		for _, path := range []string{"/favicon.ico", "/apple-touch-icon.png"} {
			code, h, body := request(t, s, "GET", path, nil, "")
			if code != 200 || h.Get("Content-Type") != "image/png" || !bytes.HasPrefix(body, png) {
				t.Errorf("%s %s: %d %s", srv, path, code, h.Get("Content-Type"))
			}
		}
	}

	_, _, home := request(t, private, "GET", "/", nil, "")
	for _, want := range []string{`<link rel="manifest" href="/manifest.webmanifest">`, `<link rel="apple-touch-icon" href="/_ui/icon-180.`, `<link rel="icon" href="/_ui/icon-32.`, `content="Cairn"`} {
		if !strings.Contains(string(home), want) {
			t.Errorf("owner page lacks %s", want)
		}
	}
	// The icon the head names is served, from both listeners.
	start := strings.Index(string(home), `/_ui/icon-180.`)
	iconPath := string(home)[start : start+strings.Index(string(home)[start:], `"`)]
	for _, s := range []string{private.URL, public.URL} {
		srv := private
		if s == public.URL {
			srv = public
		}
		if code, h, _ := request(t, srv, "GET", iconPath, nil, ""); code != 200 || !strings.Contains(h.Get("Cache-Control"), "immutable") {
			t.Errorf("%s %s: %d", s, iconPath, code)
		}
	}

	code, h, body := request(t, private, "GET", "/manifest.webmanifest", nil, "")
	var m struct {
		StartURL string `json:"start_url"`
		Icons    []struct{ Src, Sizes, Purpose string }
	}
	if code != 200 || h.Get("Content-Type") != "application/manifest+json" || json.Unmarshal(body, &m) != nil {
		t.Fatalf("manifest: %d %s %s", code, h.Get("Content-Type"), body)
	}
	if !strings.Contains(string(body), `"display":"standalone"`) || !strings.Contains(string(body), `"name":"Cairn"`) || len(m.Icons) != 3 || m.StartURL != "/" {
		t.Errorf("manifest: %s", body)
	}

	// Visitors get the icons but no install offer, and no manifest at all.
	_, _, shared := request(t, public, "GET", "/router", nil, "")
	if !strings.Contains(string(shared), `rel="icon"`) || !strings.Contains(string(shared), `rel="apple-touch-icon"`) {
		t.Error("shared page lacks its icons")
	}
	if strings.Contains(string(shared), "manifest") || strings.Contains(string(shared), "apple-mobile-web-app") {
		t.Error("shared page offers to install the library")
	}
	if code, _, _ := request(t, public, "GET", "/manifest.webmanifest", nil, ""); code != 404 {
		t.Errorf("public manifest: %d", code)
	}
}
