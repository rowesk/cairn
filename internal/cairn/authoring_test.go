package cairn_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"github.com/rowesk/cairn/internal/cairn"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func authorForm(t *testing.T, srv *httptest.Server, path string, v url.Values, origin string) (int, http.Header, string) {
	t.Helper()
	_, _, body := request(t, srv, "GET", "/new", nil, "")
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(body)
	v.Set("csrf", string(m[1]))
	req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	c := *srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, e := c.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, r.Header, string(b)
}
func importFile(t *testing.T, srv *httptest.Server, name string, data []byte, slug string) (int, string) {
	t.Helper()
	_, _, body := request(t, srv, "GET", "/import", nil, "")
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(body)
	var b bytes.Buffer
	f := multipart.NewWriter(&b)
	f.WriteField("csrf", string(m[1]))
	f.WriteField("slug", slug)
	w, _ := f.CreateFormFile("document", name)
	w.Write(data)
	f.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/import", &b)
	req.Header.Set("Content-Type", f.FormDataContentType())
	req.Header.Set("Origin", srv.URL)
	c := *srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, e := c.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	out, _ := io.ReadAll(r.Body)
	return r.StatusCode, string(out)
}
func TestMarkdownAuthoring(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	values := url.Values{"title": {"My note"}, "markdown": {"# Hello\n\n**Saved**\n\n```mermaid\ngraph TD; A-->B\n```"}}
	if code, _, _ := authorForm(t, srv, "/new", values, "https://other.example"); code != 403 {
		t.Fatal(code)
	}
	code, _, body := authorForm(t, srv, "/new?preview=1", values, srv.URL)
	if code != 200 || !strings.Contains(body, "<strong>Saved</strong>") || !strings.Contains(body, "/_runtime/mermaid.js") {
		t.Fatal(code, body)
	}
	_, _, list := request(t, srv, "GET", "/", nil, "")
	if strings.Contains(string(list), "My note") {
		t.Fatal("preview persisted")
	}
	code, h, body := authorForm(t, srv, "/new", values, srv.URL)
	if code != 303 {
		t.Fatal(code, body)
	}
	code, h, b := request(t, srv, "GET", h.Get("Location")+"?download=1", nil, "")
	if code != 200 || string(b) != values.Get("markdown") || !strings.Contains(h.Get("Content-Disposition"), "attachment") {
		t.Fatal("markdown download")
	}
	code, _, body = authorForm(t, srv, "/new", url.Values{"title": {""}, "markdown": {"keep my writing"}}, srv.URL)
	if code != 400 || !strings.Contains(body, "keep my writing") {
		t.Fatal("validation lost writing")
	}
}
func TestImportedFilesAndDownloadAccess(t *testing.T) {
	app, e := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if e != nil {
		t.Fatal(e)
	}
	defer app.Close()
	private := httptest.NewServer(app)
	defer private.Close()
	public := httptest.NewServer(app.PublicHandler())
	defer public.Close()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	var doc bytes.Buffer
	z := zip.NewWriter(&doc)
	f, _ := z.Create("word/document.xml")
	f.Write([]byte(`<w:document xmlns:w="w"><w:p><w:r><w:t>Word text &amp; more</w:t></w:r></w:p></w:document>`))
	z.Close()
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"notes.txt", []byte("<script>alert(1)</script>"), "&lt;script&gt;"},
		{"script.php", []byte("<?php system('id'); ?><script>alert(1)</script>"), "&lt;?php"},
		{"values.json", []byte(`{"a":1}`), "&#34;a&#34;"},
		{"flow.mmd", []byte("graph TD; A-->B"), "/_runtime/mermaid.js"},
		{"photo.png", png, "?asset=image.png"},
		{"document.docx", doc.Bytes(), "Word text &amp; more"},
		{"document.pdf", []byte("%PDF-1.4\nfixture"), "Download original"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := strings.ReplaceAll(tc.name, ".", "-")
			if c, b := importFile(t, private, tc.name, tc.data, slug); c != 303 {
				t.Fatal(c, b)
			}
			c, _, b := request(t, private, "GET", "/_content/"+slug, nil, "")
			if c != 200 || !strings.Contains(string(b), tc.want) {
				t.Fatal(c, string(b))
			}
			c, h, b := request(t, private, "GET", "/"+slug+"?download=1", nil, "")
			if c != 200 || !bytes.Equal(b, tc.data) || h.Get("Content-Type") != "application/octet-stream" || !strings.Contains(h.Get("Content-Disposition"), "attachment") {
				t.Fatal("download changed or executable")
			}
		})
	}
	slug := "script-php"
	if c, _, _ := request(t, public, "GET", "/"+slug+"?download=1", nil, ""); c != 404 {
		t.Fatal("private source leaked")
	}
	if c := ownerPost(t, private, slug, url.Values{"action": {"share"}, "public_slug": {"source"}}); c != 303 {
		t.Fatal(c)
	}
	if c, _, _ := request(t, public, "GET", "/source?download=1", nil, ""); c != 200 {
		t.Fatal(c)
	}
	if c := ownerPost(t, private, slug, url.Values{"action": {"password"}, "password": {"secret"}}); c != 303 {
		t.Fatal(c)
	}
	if c, _, _ := request(t, public, "GET", "/source?download=1", nil, ""); c == 200 {
		t.Fatal("password bypass")
	}
	ownerPost(t, private, slug, url.Values{"action": {"unshare"}})
	if c, _, _ := request(t, public, "GET", "/source?download=1", nil, ""); c != 404 {
		t.Fatal("revoked source leaked")
	}
	ownerPost(t, private, slug+"/delete", url.Values{"action": {"delete"}})
	if c, _, _ := request(t, private, "GET", "/"+slug+"?download=1", nil, ""); c != 404 {
		t.Fatal("deleted source leaked")
	}
	if c, _ := importFile(t, private, "not.png", []byte("<?php bad ?>"), "fake-image"); c != 400 {
		t.Fatal("accepted fake image")
	}
	if c, _ := importFile(t, private, "bad.exe", []byte("exe"), "executable"); c != 400 {
		t.Fatal("accepted executable")
	}
}

func TestEditorShortlinksSharingAndPastedImages(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	private := httptest.NewServer(app)
	defer private.Close()
	public := httptest.NewServer(app.PublicHandler())
	defer public.Close()
	image := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="
	values := url.Values{"title": {"With an image"}, "markdown": {"![Image](?asset=pasted.png)"}, "images": {`{"pasted.png":"` + image + `"}`}, "slug": {"my-page"}, "access": {"public"}}
	code, _, body := authorForm(t, private, "/new?preview=1", values, private.URL)
	if code != 200 || !strings.Contains(body, "data:image/png;base64,") || strings.Contains(body, `href="?asset=pasted.png"`) {
		t.Fatal("preview", code, body)
	}
	if c, _, _ := request(t, public, "GET", "/my-page", nil, ""); c != 404 {
		t.Fatal("preview published")
	}
	code, h, body := authorForm(t, private, "/new", values, private.URL)
	if code != 303 || h.Get("Location") != "/my-page" {
		t.Fatal(code, body)
	}
	for _, route := range []string{"/my-page", "/_content/my-page?asset=pasted.png", "/my-page?download=1"} {
		if c, _, _ := request(t, public, "GET", route, nil, ""); c != 200 {
			t.Fatal(route, c)
		}
	}
	code, _, body = authorForm(t, private, "/new", values, private.URL)
	if code != 409 || !strings.Contains(body, "pasted.png") {
		t.Fatal("conflict lost images", code)
	}
	ownerPost(t, private, "my-page", url.Values{"action": {"unshare"}})
	if c, _, _ := request(t, public, "GET", "/_content/my-page?asset=pasted.png", nil, ""); c != 404 {
		t.Fatal("revoked image leaked")
	}
	values.Set("slug", "private-page")
	values.Set("access", "private")
	if c, _, _ := authorForm(t, private, "/new", values, private.URL); c != 303 {
		t.Fatal(c)
	}
	if c, _, _ := request(t, public, "GET", "/private-page", nil, ""); c != 404 {
		t.Fatal("private editor page leaked")
	}
	values.Set("slug", "bad-image")
	values.Set("images", `{"pasted.png":"PD9waHA="}`)
	if c, _, _ := authorForm(t, private, "/new", values, private.URL); c != 400 {
		t.Fatal("accepted fake paste")
	}
	unavailable, close := start(t, t.TempDir())
	defer close()
	values.Del("images")
	values.Set("access", "public")
	if c, _, _ := authorForm(t, unavailable, "/new", values, unavailable.URL); c != 503 {
		t.Fatal("unconfigured public save", c)
	}
	if c, _, _ := request(t, unavailable, "GET", "/bad-image", nil, ""); c != 404 {
		t.Fatal("failed publication persisted")
	}
}
