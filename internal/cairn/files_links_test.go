package cairn_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFilesAndLinksCreationSharing(t *testing.T) {
	private, public := publicPublishingServers(t)
	c, _, b := authorForm(t, private, "/settings", url.Values{"action": {"key_create"}, "name": {"File agent"}}, private.URL)
	var key struct{ Key string }
	if c != 200 || json.Unmarshal([]byte(b), &key) != nil || key.Key == "" {
		t.Fatal(c, b)
	}
	for _, shared := range []bool{false, true} {
		slug := fmt.Sprintf("agent-file-%t", shared)
		fields := [][2]string{{"slug", slug}, {"original_request", "PRIVATE-FILE"}}
		if shared {
			fields = append(fields, [2]string{"share_enabled", "true"})
		}
		c, b := sendFile(t, private, "POST", "/api/files", fields, []filePart{{"file", "a.png", strings.NewReader("image bytes")}}, map[string]string{"Authorization": "Bearer " + key.Key})
		if c != 201 {
			t.Fatal(c, string(b))
		}
		want := 404
		if shared {
			want = 200
		}
		if c, _, _ := request(t, public, "GET", "/"+slug+".png", nil, ""); c != want {
			t.Fatal(slug, c)
		}
		link := fmt.Sprintf("agent-link-%t", shared)
		in := map[string]any{"url": "https://example.org/path", "slug": link}
		if shared {
			in["share_enabled"] = true
		}
		if c, _, b := request(t, private, "POST", "/api/links", in, key.Key); c != 201 {
			t.Fatal(c, string(b))
		}
		c, _, _, _ = fileLinkCall(t, public, "GET", "/"+link, nil, nil)
		want = 404
		if shared {
			want = 302
		}
		if c != want {
			t.Fatal(link, c)
		}
	}
	if c, b := ownerImport(t, private, "owner.zip", []byte("PKfixture"), [2]string{"slug", "owner-file"}); c != 303 {
		t.Fatal(c, b)
	}
	if c, _, _ := request(t, public, "GET", "/owner-file.zip", nil, ""); c != 200 {
		t.Fatal("owner file not shared", c)
	}
	c, _, b = postForm(t, private, "/new-link", url.Values{"csrf": {formToken(t, private)}, "url": {"https://example.org/owner"}, "slug": {"owner-link"}}, private.URL)
	if c != 303 {
		t.Fatal(c, b)
	}
	if c, _, _, _ := fileLinkCall(t, public, "GET", "/owner-link", nil, nil); c != 302 {
		t.Fatal("owner link not shared", c)
	}
}

func TestDirectFileAddresses(t *testing.T) {
	private, public := publicPublishingServers(t)
	for i, tc := range []struct {
		ext, mime string
		inline    bool
	}{
		{"png", "image/png", true}, {"pdf", "application/pdf", true}, {"mp4", "video/mp4", true}, {"mp3", "audio/mpeg", true}, {"html", "text/html", true}, {"svg", "image/svg+xml", true}, {"zip", "application/octet-stream", false}, {"txt", "text/plain", true},
	} {
		slug := fmt.Sprintf("direct-%d", i)
		data := []byte("fixture bytes for ranges")
		if c, b := publishFilePage(t, private, "POST", "/api/files", "file."+tc.ext, data, [2]string{"slug", slug}, [2]string{"share_enabled", "true"}); c != 201 {
			t.Fatal(c, string(b))
		}
		for _, srv := range []struct {
			path   string
			public bool
		}{{"/" + slug + "." + tc.ext, false}, {"/" + slug + "." + strings.ToUpper(tc.ext), true}} {
			server := private
			if srv.public {
				server = public
			}
			c, h, b := request(t, server, "GET", srv.path, nil, "")
			if c != 200 || !bytes.Equal(b, data) || !strings.HasPrefix(h.Get("Content-Type"), tc.mime) || strings.HasPrefix(h.Get("Content-Disposition"), "inline") != tc.inline {
				t.Fatal(c, h, string(b))
			}
			if tc.ext == "html" || tc.ext == "svg" {
				policy := h.Get("Content-Security-Policy")
				for _, v := range []string{"sandbox ", "connect-src 'none'", "form-action 'none'"} {
					if !strings.Contains(policy, v) {
						t.Fatal(policy)
					}
				}
				if strings.Contains(policy, "allow-same-origin") {
					t.Fatal(policy)
				}
			}
		}
		c, h, _, b := fileLinkCall(t, public, "GET", "/"+slug+"."+tc.ext, nil, map[string]string{"Range": "bytes=2-5"})
		if c != 206 || b != string(data[2:6]) || h.Get("Content-Range") == "" {
			t.Fatal(c, h, b)
		}
		if c, _, _ := request(t, public, "GET", "/"+slug+".wrong", nil, ""); c != 404 {
			t.Fatal(c)
		}
		if c := ownerPost(t, private, slug, url.Values{"action": {"password"}, "password": {"long-password"}}); c != 303 {
			t.Fatal(c)
		}
		if c, _, _ := request(t, public, "GET", "/"+slug+"."+tc.ext, nil, ""); c != 401 {
			t.Fatal("password bypass", c)
		}
		c, h, _, _ = fileLinkCall(t, public, "POST", "/"+slug+"."+tc.ext, "password=long-password", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": public.URL})
		if c != 303 || h.Get("Location") != "/"+slug+"."+tc.ext || len(h.Values("Set-Cookie")) != 1 {
			t.Fatal(c, h)
		}
		cookie := strings.Split(h.Get("Set-Cookie"), ";")[0]
		if c, _, _, b := fileLinkCall(t, public, "GET", "/"+slug+"."+tc.ext, nil, map[string]string{"Cookie": cookie}); c != 200 || b != string(data) {
			t.Fatal(c, b)
		}
		ownerPost(t, private, slug, url.Values{"action": {"unshare"}})
		if c, _, _ := request(t, public, "GET", "/"+slug+"."+tc.ext, nil, ""); c != 404 {
			t.Fatal(c)
		}
	}
}

func TestShortLinkValidationAndAccess(t *testing.T) {
	a := startAPI(t, "")
	for _, target := range []string{"javascript:alert(1)", "data:text/html,hello", "ftp://example.org/a", "https://reports.example.com/loop", "https://user:pass@example.org", "https://example.org/" + strings.Repeat("a", 2048)} {
		c, _, b := request(t, a.private, "POST", "/api/links", map[string]any{"url": target}, token)
		if c != 400 {
			t.Fatal(target, c, string(b))
		}
	}
	if c, _, _ := request(t, a.private, "POST", "/api/links", map[string]any{"url": "https://example.org", "password": "long-password"}, token); c != 400 {
		t.Fatal(c)
	}
	if c, _, b := request(t, a.private, "POST", "/api/links", map[string]any{"url": "https://example.org/target", "slug": "short", "share_enabled": true}, token); c != 201 {
		t.Fatal(c, string(b))
	}
	for _, method := range []string{"GET", "HEAD", "GET"} {
		c, h, _, b := fileLinkCall(t, a.public, method, "/short", nil, nil)
		if c != 302 || h.Get("Location") != "https://example.org/target" || b != "" {
			t.Fatal(c, h, b)
		}
	}
	if c := ownerPost(t, a.private, "short", url.Values{"action": {"password"}, "password": {"long-password"}}); c != 400 {
		t.Fatal(c)
	}
	ownerPost(t, a.private, "short", url.Values{"action": {"unshare"}})
	if c, _, _, _ := fileLinkCall(t, a.public, "GET", "/short", nil, nil); c != 404 {
		t.Fatal(c)
	}
}

func TestPublicPreviewPrivacy(t *testing.T) {
	private, public := publicPublishingServers(t)
	in := page("Secret title for previews")
	in["slug"] = "preview-check"
	in["share_enabled"] = true
	in["original_request"] = "PRIVATE-PREVIEW"
	delete(in, "html")
	in["markdown"] = "# Body title\n\n" + strings.Repeat("Some public text. ", 30)
	if c, _, b := request(t, private, "POST", "/api/pages", in, token); c != 201 {
		t.Fatal(c, string(b))
	}
	_, _, b := request(t, public, "GET", "/preview-check", nil, "")
	for _, v := range []string{`property="og:title" content="Secret title for previews"`, `name="twitter:card"`, `property="og:url" content="https://reports.example.com/preview-check"`, `property="og:image"`} {
		if !strings.Contains(string(b), v) {
			t.Fatal(v)
		}
	}
	if strings.Contains(string(b), "PRIVATE-PREVIEW") {
		t.Fatal("request leaked")
	}
	ownerPost(t, private, "preview-check", url.Values{"action": {"password"}, "password": {"long-password"}})
	_, _, b = request(t, public, "GET", "/preview-check", nil, "")
	if strings.Contains(string(b), "Secret title for previews") || strings.Contains(string(b), "Some public text") || !strings.Contains(string(b), `property="og:title" content="Protected page"`) {
		t.Fatal("protected preview leaked")
	}
}

func fileLinkCall(t *testing.T, srv *httptest.Server, method, path string, body any, headers map[string]string) (int, http.Header, map[string]any, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(body.(string))
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, res.Header, nil, string(b)
}
