package cairn

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebImageAddressGuard(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "100.127.255.254", "0.0.0.0", "224.0.0.1", "240.0.0.1", "192.0.2.1", "198.18.0.1", "::", "::1", "fc00::1", "fe80::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:100.64.0.1", "::ffff:192.168.1.1", "2001:db8::1", "64:ff9b::7f00:1"} {
		host := ip
		if strings.Contains(ip, ":") {
			host = "[" + ip + "]"
		}
		if publicImageAddress(host + ":80") {
			t.Errorf("allowed %s", ip)
		}
	}
	for _, addr := range []string{"8.8.8.8:80", "1.1.1.1:443", "[2606:4700:4700::1111]:443", "[::ffff:8.8.8.8]:80"} {
		if !publicImageAddress(addr) {
			t.Errorf("refused %s", addr)
		}
	}
	for _, raw := range []string{"http://localhost:8080/a", "ftp://example.com/a", "http://user:pass@example.com/a"} {
		u, _ := url.Parse(raw)
		if checkWebImageURL(u) == nil {
			t.Errorf("allowed %s", raw)
		}
	}
}

func TestWebImagesSaveAndReuse(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	hits := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.UserAgent() != "Cairn" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected fetch headers")
		}
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "text/plain")
			w.Write(png)
		case "/large":
			io.Copy(w, io.LimitReader(&repeatingReader{}, maxAssetBytes+1))
		case "/text":
			w.Write([]byte("not an image"))
		case "/redirect":
			http.Redirect(w, r, "http://127.0.0.1/no", 302)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	// Allow only this fixture's address and port. The redirect to port 80
	// still reaches the real address guard and must fail before connecting.
	oldAddress, oldURL := webImageAddressAllowed, webImageURLAllowed
	fixtureURL, _ := url.Parse(fixture.URL)
	webImageAddressAllowed = func(address string) bool { return address == fixtureURL.Host || publicImageAddress(address) }
	webImageURLAllowed = func(u *url.URL) error {
		if u.Host == fixtureURL.Host {
			return nil
		}
		return checkWebImageURL(u)
	}
	defer func() { webImageAddressAllowed, webImageURLAllowed = oldAddress, oldURL }()
	s, err := Open(Config{DataDir: t.TempDir(), PublisherToken: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	save := func(slug, markdown string, replace bool) *httptest.ResponseRecorder {
		t.Helper()
		method, path := "POST", "/api/pages"
		if replace {
			method, path = "PUT", "/api/pages/"+slug
		}
		r := httptest.NewRequest(method, path, nil)
		in := publication{Slug: slug, Title: "Images", Markdown: markdown, Agent: "Owner", OwnerWritten: true, Source: &sourceFile{Name: "page.md", Data: []byte(markdown)}}
		w := httptest.NewRecorder()
		s.savePublication(w, r, in)
		if w.Code != 201 && w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body)
		}
		return w
	}
	src := fixture.URL + "/ok"
	markdown := "![Ridge](" + src + " \"Caption\")\n\n<img src=\"" + src + "\" alt=\"Again\">"
	save("web-images", markdown, false)
	p, err := s.getPage("web-images")
	if err != nil {
		t.Fatal(err)
	}
	name := webImagePrefix(src) + ".png"
	data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html"))
	if err != nil || !strings.Contains(string(data), `src="?asset=`+name+`"`) || !strings.Contains(string(data), `href="?asset=`+name+`"`) || !strings.Contains(string(data), "<figcaption>Caption</figcaption>") {
		t.Fatalf("HTML: %s %v", data, err)
	}
	source, _ := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "source.bin"))
	if string(source) != markdown {
		t.Fatal("source changed")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/_content/web-images?asset="+name, nil))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("asset: %d", w.Code)
	}
	// Load and post the actual editor form for the replacement.
	target, err := s.loadEditTarget("web-images")
	if err != nil {
		t.Fatal(err)
	}
	token := s.formToken()
	form := url.Values{"title": {"Images"}, "markdown": {markdown}, "csrf": {token.String()}, "access": {"private"}}
	req := httptest.NewRequest("POST", "http://localhost/edit/web-images", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://localhost")
	out := httptest.NewRecorder()
	s.editor(out, req, target)
	if out.Code != 303 {
		t.Fatalf("editor: %d %s", out.Code, out.Body)
	}
	if hits != 1 {
		t.Fatalf("fetched %d times", hits)
	}
	for _, tc := range []struct{ path, reason string }{{"missing", "HTTP 404"}, {"large", "too large"}, {"text", "not an image"}, {"redirect", "refused address"}} {
		t.Run(tc.path, func(t *testing.T) {
			src := fixture.URL + "/" + tc.path
			w := save("fail-"+tc.path, "![Fallback]("+src+")", false)
			var reply struct{ Warnings []apiWarning }
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, warning := range reply.Warnings {
				if warning.Code == "web_image_failed" {
					count++
					if !strings.Contains(warning.Message, src+": "+tc.reason) {
						t.Fatal(warning.Message)
					}
				}
			}
			if count != 1 {
				t.Fatal(reply.Warnings)
			}
			p, _ := s.getPage("fail-" + tc.path)
			body, _ := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html"))
			if strings.Contains(string(body), "<img") || !strings.Contains(string(body), ">Fallback</a>") || !strings.Contains(string(body), src) {
				t.Fatalf("fallback: %s", body)
			}
		})
	}
}

type repeatingReader struct{}

func (*repeatingReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
