package cairn_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rowesk/cairn/internal/cairn"
)

type filePart struct {
	field, name string
	data        io.Reader
}

// fileBody streams a multipart body through a pipe, so large parts are
// generated on the fly instead of being held in memory.
func fileBody(fields [][2]string, files []filePart) (io.Reader, string) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		for _, f := range fields {
			if err := mw.WriteField(f[0], f[1]); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		for _, f := range files {
			w, err := mw.CreateFormFile(f.field, f.name)
			if err == nil {
				_, err = io.Copy(w, f.data)
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.CloseWithError(mw.Close())
	}()
	return pr, mw.FormDataContentType()
}

func sendFile(t *testing.T, srv *httptest.Server, method, path string, fields [][2]string, files []filePart, headers map[string]string) (int, []byte) {
	t.Helper()
	body, contentType := fileBody(fields, files)
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func publishFilePage(t *testing.T, srv *httptest.Server, method, path, name string, data []byte, fields ...[2]string) (int, []byte) {
	t.Helper()
	fields = append([][2]string{{"agent", "FileAgent"}, {"original_request", "Keep this file"}}, fields...)
	return sendFile(t, srv, method, path, fields, []filePart{{"file", name, bytes.NewReader(data)}}, nil)
}

// pattern yields deterministic bytes without allocating the whole file.
type pattern struct{ n int64 }

func (p *pattern) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = byte(p.n*31 + p.n>>9)
		p.n++
	}
	return len(b), nil
}
func patterned(n int64) io.Reader { return io.LimitReader(&pattern{}, n) }

func apiError(t *testing.T, body []byte) cairn.APIError {
	t.Helper()
	var e cairn.APIError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("not an API error: %s", body)
	}
	return e
}

func dirCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// ownerImport posts the /import form with arbitrary extra fields.
func ownerImport(t *testing.T, srv *httptest.Server, name string, data []byte, fields ...[2]string) (int, string) {
	t.Helper()
	_, _, page := request(t, srv, "GET", "/import", nil, "")
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
	body, contentType := fileBody(append([][2]string{{"csrf", string(m[1])}}, fields...), []filePart{{"document", name, bytes.NewReader(data)}})
	req, _ := http.NewRequest("POST", srv.URL+"/import", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", srv.URL)
	c := *srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func zipLike(n int) []byte {
	data := make([]byte, n)
	copy(data, "PK\x03\x04")
	for i := 4; i < n; i++ {
		data[i] = byte(i * 7)
	}
	return data
}

func TestFilePageAPIPublishReadAndDownload(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	data := zipLike(3000)
	code, body := publishFilePage(t, srv, "POST", "/api/files", "Q3 numbers.xlsx", data, [2]string{"slug", "q3"})
	if code != 201 {
		t.Fatal(code, string(body))
	}
	var result struct{ ID, URL string }
	if json.Unmarshal(body, &result); result.URL != "/q3" {
		t.Fatalf("result: %s", body)
	}
	_, _, reader := request(t, srv, "GET", "/q3", nil, "")
	for _, want := range []string{`class="file-page"`, "Q3 numbers.xlsx", "Excel spreadsheet", "2.9 KB", "/q3?download=1", `>Q3 numbers</h1>`} {
		if !strings.Contains(string(reader), want) {
			t.Errorf("reader lacks %q", want)
		}
	}
	if strings.Contains(string(reader), "<iframe") {
		t.Error("file page renders the report iframe")
	}
	code, h, got := request(t, srv, "GET", "/q3?download=1", nil, "")
	if code != 200 || !bytes.Equal(got, data) || h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Disposition") != `attachment; filename="Q3 numbers.xlsx"` {
		t.Fatalf("download: %d %v len=%d", code, h, len(got))
	}
	if _, _, pages := request(t, srv, "GET", "/", nil, ""); strings.Contains(string(pages), `data-slug="q3"`) {
		t.Fatal("file appears under Pages")
	}
	_, _, list := request(t, srv, "GET", "/?kind=files", nil, "")
	// An agent's file stays private until the owner shares it.
	if strings.Contains(string(list), `data-shared`) || !strings.Contains(string(list), "doc-mark file-mark") || !strings.Contains(string(list), "Q3 numbers.xlsx · <span class=\"file-row-size\">2.9 KB</span>") {
		t.Fatal("library row is not a file row")
	}
	// An explicit title wins over the filename.
	if code, body := publishFilePage(t, srv, "POST", "/api/files", "raw.zip", data, [2]string{"title", "Raw exports"}, [2]string{"slug", "raw"}); code != 201 {
		t.Fatal(code, string(body))
	}
	if _, _, r := request(t, srv, "GET", "/raw", nil, ""); !strings.Contains(string(r), `>Raw exports</h1>`) || !strings.Contains(string(r), "ZIP archive") {
		t.Fatal("explicit title")
	}
}

func TestFilePageAPIErrors(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	defer close()
	meta := [][2]string{{"agent", "FileAgent"}, {"original_request", "Keep this file"}}
	one := func() []filePart { return []filePart{{"file", "a.zip", strings.NewReader("PK")}} }
	for _, tc := range []struct {
		name    string
		fields  [][2]string
		files   []filePart
		headers map[string]string
		status  int
		code    string
		field   string
	}{
		{"missing file", meta, nil, nil, 400, cairn.ErrValidationFailed, "file"},
		{"two files", meta, append(one(), filePart{"file", "b.zip", strings.NewReader("PK")}), nil, 400, cairn.ErrValidationFailed, ""},
		{"other file part", meta, append(one(), filePart{"extra", "b.zip", strings.NewReader("PK")}), nil, 400, cairn.ErrValidationFailed, ""},
		{"unknown field", append(meta, [2]string{"html", "<p>x</p>"}), one(), nil, 400, cairn.ErrValidationFailed, "html"},
		{"empty file", meta, []filePart{{"file", "a.zip", strings.NewReader("")}}, nil, 400, cairn.ErrValidationFailed, "file"},
		{"json body", meta, one(), map[string]string{"Content-Type": "application/json"}, 415, cairn.ErrUnsupportedMediaType, ""},
		{"no token", meta, one(), map[string]string{"Authorization": ""}, 401, cairn.ErrAuthenticationRequired, ""},
		{"wrong token", meta, one(), map[string]string{"Authorization": "Bearer wrong"}, 401, cairn.ErrAuthenticationRequired, ""},
		{"browser origin", meta, one(), map[string]string{"Origin": srv.URL}, 403, cairn.ErrBrowserOriginForbidden, ""},
		{"browser fetch", meta, one(), map[string]string{"Sec-Fetch-Site": "same-origin"}, 403, cairn.ErrBrowserOriginForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := sendFile(t, srv, "POST", "/api/files", tc.fields, tc.files, tc.headers)
			e := apiError(t, body)
			if status != tc.status || e.Code != tc.code || (tc.field != "" && e.Field != tc.field) {
				t.Fatalf("got %d %s", status, body)
			}
		})
	}
	if status, _ := publishFilePage(t, srv, "PUT", "/api/files/missing", "a.zip", []byte("PK")); status != 404 {
		t.Fatalf("replace missing: %d", status)
	}
	if n := dirCount(t, filepath.Join(dir, "uploads")); n != 0 {
		t.Fatalf("%d leaked uploads", n)
	}
	if _, _, list := request(t, srv, "GET", "/", nil, ""); strings.Contains(string(list), `class="doc-mark file-mark"`) {
		t.Fatal("rejected upload created a page")
	}
}

func TestFilePageSizeCap(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	defer close()
	const limit = 50 << 20
	meta := [][2]string{{"agent", "FileAgent"}, {"original_request", "Big file"}, {"slug", "big"}}
	status, body := sendFile(t, srv, "POST", "/api/files", meta, []filePart{{"file", "big.bin", patterned(limit)}}, nil)
	if status != 201 {
		t.Fatalf("exact limit: %d %s", status, body)
	}
	meta[2][1] = "too-big"
	status, body = sendFile(t, srv, "POST", "/api/files", meta, []filePart{{"file", "big.bin", patterned(limit + 1)}}, nil)
	if status != 413 || apiError(t, body).Code != cairn.ErrPayloadTooLarge {
		t.Fatalf("over limit: %d %s", status, body)
	}
	if c, _, _ := request(t, srv, "GET", "/too-big", nil, ""); c != 404 {
		t.Fatal("oversized upload saved")
	}
	res, err := srv.Client().Get(srv.URL + "/big?download=1")
	if err != nil {
		t.Fatal(err)
	}
	got, want := sha256.New(), sha256.New()
	n, _ := io.Copy(got, res.Body)
	res.Body.Close()
	io.Copy(want, patterned(limit))
	if n != limit || !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatalf("download of %d bytes differs", n)
	}
	if n := dirCount(t, filepath.Join(dir, "uploads")); n != 0 {
		t.Fatalf("%d leaked uploads", n)
	}
	if n := dirCount(t, filepath.Join(dir, "objects")); n != 1 {
		t.Fatalf("%d objects for 1 page", n)
	}
}

func TestFilePageImportRouting(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	large := append(append([]byte{}, png...), make([]byte, 5<<20)...)
	for _, tc := range []struct {
		name, slug string
		data       []byte
		asFile     bool
		file       bool
	}{
		{"huge.png", "huge", large, false, true},
		{"small.png", "small", png, false, true},
		{"notes.md", "notes", []byte("# Notes\n\nKeep me."), true, true},
		{"cover.psd", "cover", []byte("8BPS fixture"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := [][2]string{{"slug", tc.slug}}
			if tc.asFile {
				fields = append(fields, [2]string{"as_file", "1"})
			}
			if c, b := ownerImport(t, srv, tc.name, tc.data, fields...); c != 303 {
				t.Fatal(c, b)
			}
			_, _, reader := request(t, srv, "GET", "/"+tc.slug, nil, "")
			if strings.Contains(string(reader), `class="file-page"`) != tc.file || strings.Contains(string(reader), "<iframe") == tc.file {
				t.Fatalf("file page = %v expected", tc.file)
			}
			c, _, got := request(t, srv, "GET", "/"+tc.slug+"?download=1", nil, "")
			if c != 200 || !bytes.Equal(got, tc.data) {
				t.Fatal("download differs")
			}
		})
	}
	if _, _, b := request(t, srv, "GET", "/_content/small", nil, ""); !strings.Contains(string(b), "Open this page in Cairn to download the file.") {
		t.Fatal("small image is not a file page")
	}
	if _, _, b := request(t, srv, "GET", "/cover", nil, ""); !strings.Contains(string(b), "Photoshop document") {
		t.Fatal("psd type")
	}
}

func createdAt(t *testing.T, reader []byte) string {
	t.Helper()
	m := regexp.MustCompile(`<time datetime="([^"]+)"`).FindSubmatch(reader)
	if m == nil {
		t.Fatal("no created time")
	}
	return string(m[1])
}

func TestFilePageReplacement(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	defer close()
	if c, b := publishFilePage(t, srv, "POST", "/api/files", "plan.pdf", []byte("%PDF old"), [2]string{"slug", "plan"}); c != 201 {
		t.Fatal(c, string(b))
	}
	_, _, before := request(t, srv, "GET", "/plan", nil, "")
	time.Sleep(1100 * time.Millisecond)
	if c, b := publishFilePage(t, srv, "PUT", "/api/files/plan", "plan-v2.pdf", []byte("%PDF new")); c != 200 && c != 201 {
		t.Fatal(c, string(b))
	}
	_, _, after := request(t, srv, "GET", "/plan", nil, "")
	if createdAt(t, before) != createdAt(t, after) || !strings.Contains(string(after), "plan-v2.pdf") {
		t.Fatal("replacement changed the created date or kept the old name")
	}
	if _, _, got := request(t, srv, "GET", "/plan?download=1", nil, ""); string(got) != "%PDF new" {
		t.Fatalf("download after replace: %q", got)
	}
	in := map[string]any{"title": "Plan", "agent": "FileAgent", "original_request": "Now a page", "markdown": "# Written plan"}
	if c, _, b := request(t, srv, "PUT", "/api/pages/plan", in, token); c != 200 && c != 201 {
		t.Fatal(c, string(b))
	}
	_, _, reader := request(t, srv, "GET", "/plan", nil, "")
	if strings.Contains(string(reader), `class="file-page"`) || !strings.Contains(string(reader), "<iframe") {
		t.Fatal("markdown replacement still renders a file page")
	}
	if _, _, list := request(t, srv, "GET", "/", nil, ""); strings.Contains(string(list), `class="doc-mark file-mark"`) || strings.Contains(string(list), "plan-v2.pdf") {
		t.Fatal("library still shows a file row")
	}
	if c, _, b := request(t, srv, "GET", "/plan?download=1", nil, ""); c != 200 || string(b) != "# Written plan" {
		t.Fatalf("old file still downloadable: %d", c)
	}
	if n := dirCount(t, filepath.Join(dir, "objects")); n != 1 {
		t.Fatalf("%d objects after replacements", n)
	}
}

func TestFilePagePublicSharing(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	private := httptest.NewServer(app)
	defer private.Close()
	public := httptest.NewTLSServer(app.PublicHandler())
	defer public.Close()
	data := zipLike(5000)
	if c, b := publishFilePage(t, private, "POST", "/api/files", "bundle.zip", data, [2]string{"slug", "bundle"}); c != 201 {
		t.Fatal(c, string(b))
	}
	if c, _, _ := request(t, public, "GET", "/files?download=1", nil, ""); c != 404 {
		t.Fatal("unshared file leaked")
	}
	if c := ownerPost(t, private, "bundle", url.Values{"action": {"share"}, "public_slug": {"files"}}); c != 303 {
		t.Fatal(c)
	}
	c, _, reader := request(t, public, "GET", "/files", nil, "")
	if c != 200 || !strings.Contains(string(reader), `class="file-page"`) || !strings.Contains(string(reader), "bundle.zip") || !strings.Contains(string(reader), "/files?download=1") || strings.Contains(string(reader), "Keep this file") {
		t.Fatalf("public reader: %d", c)
	}
	if c, h, got := request(t, public, "GET", "/files?download=1", nil, ""); c != 200 || !bytes.Equal(got, data) || !strings.Contains(h.Get("Content-Disposition"), "bundle.zip") {
		t.Fatalf("public download: %d", c)
	}
	if c := ownerPost(t, private, "bundle", url.Values{"action": {"password"}, "password": {"secret-words"}}); c != 303 {
		t.Fatal(c)
	}
	if c, _, got := request(t, public, "GET", "/files?download=1", nil, ""); c == 200 || bytes.Equal(got, data) {
		t.Fatal("password bypass")
	}
	if _, _, page := request(t, public, "GET", "/files", nil, ""); strings.Contains(string(page), "bundle.zip") {
		t.Fatal("locked page shows the file card")
	}
	req, _ := http.NewRequest("POST", public.URL+"/files", strings.NewReader(url.Values{"password": {"secret-words"}}.Encode()))
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
		t.Fatalf("unlock: %d", res.StatusCode)
	}
	get := func(path string) (int, []byte) {
		r, _ := http.NewRequest("GET", public.URL+path, nil)
		r.AddCookie(res.Cookies()[0])
		v, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer v.Body.Close()
		b, _ := io.ReadAll(v.Body)
		return v.StatusCode, b
	}
	if c, b := get("/files"); c != 200 || !strings.Contains(string(b), `class="file-page"`) {
		t.Fatalf("unlocked reader: %d", c)
	}
	if c, b := get("/files?download=1"); c != 200 || !bytes.Equal(b, data) {
		t.Fatalf("unlocked download: %d", c)
	}
	if c := ownerPost(t, private, "bundle", url.Values{"action": {"unshare"}}); c != 303 {
		t.Fatal(c)
	}
	for _, path := range []string{"/files", "/files?download=1"} {
		if c, _ := get(path); c != 404 {
			t.Fatalf("revoked %s: %d", path, c)
		}
	}
}

func TestFilePageDownloadReleasesLock(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	if c, b := publishFilePage(t, srv, "POST", "/api/files", "large.zip", zipLike(8<<20), [2]string{"slug", "large"}); c != 201 {
		t.Fatal(c, string(b))
	}
	// A dedicated transport so the slow download owns its own connection.
	slow := &http.Client{Transport: &http.Transport{}}
	defer slow.Transport.(*http.Transport).CloseIdleConnections()
	res, err := slow.Get(srv.URL + "/large?download=1")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(res.Body, head); err != nil || string(head) != "PK\x03\x04" {
		t.Fatalf("download start: %v %q", err, head)
	}
	done := make(chan int, 1)
	go func() {
		in := page("During download")
		in["slug"] = "during"
		body, _ := json.Marshal(in)
		req, _ := http.NewRequest("POST", srv.URL+"/api/pages", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		r, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
		if err != nil {
			done <- -1
			return
		}
		r.Body.Close()
		done <- r.StatusCode
	}()
	select {
	case c := <-done:
		if c != 201 {
			t.Fatalf("publish during download: %d", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publish blocked behind an open download")
	}
}

func TestFilePageSurvivesRestartAndWipesUploads(t *testing.T) {
	dir := t.TempDir()
	srv, close := start(t, dir)
	data := zipLike(2048)
	if c, b := publishFilePage(t, srv, "POST", "/api/files", "kept.zip", data, [2]string{"slug", "kept"}); c != 201 {
		close()
		t.Fatal(c, string(b))
	}
	close()
	stray := filepath.Join(dir, "uploads", "upload-stray")
	if err := os.WriteFile(stray, []byte("interrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	srv, close = start(t, dir)
	defer close()
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatal("stray upload survived restart")
	}
	if _, _, reader := request(t, srv, "GET", "/kept", nil, ""); !strings.Contains(string(reader), `class="file-page"`) || !strings.Contains(string(reader), "2 KB") {
		t.Fatal("file page lost after restart")
	}
	if c, _, got := request(t, srv, "GET", "/kept?download=1", nil, ""); c != 200 || !bytes.Equal(got, data) {
		t.Fatal("download lost after restart")
	}
	if _, _, list := request(t, srv, "GET", "/?kind=files", nil, ""); !strings.Contains(string(list), "kept.zip · <span class=\"file-row-size\">2 KB</span>") {
		t.Fatal("library row lost after restart")
	}
}
