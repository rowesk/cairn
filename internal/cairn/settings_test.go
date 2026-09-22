package cairn_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

func TestSettingsPersistAndControlFuturePages(t *testing.T) {
	dir := t.TempDir()
	srv, closeServer := start(t, dir)
	values := url.Values{"action": {"defaults"}, "author": {"Test author"}, "template": {"comparison"}, "link_length": {"5"}}
	if code, _, _ := authorForm(t, srv, "/settings", values, "https://elsewhere.example"); code != 403 {
		t.Fatal(code)
	}
	if code, _, _ := authorForm(t, srv, "/settings", values, srv.URL); code != 303 {
		t.Fatal(code)
	}
	values.Set("link_length", "2")
	if code, _, _ := authorForm(t, srv, "/settings", values, srv.URL); code != 400 {
		t.Fatal(code)
	}
	code, h, _ := authorForm(t, srv, "/new", url.Values{"title": {"Settings check"}, "markdown": {"Hello"}}, srv.URL)
	if code != 303 || len(strings.TrimPrefix(h.Get("Location"), "/")) != 5 {
		t.Fatal(code, h)
	}
	oldLink := h.Get("Location")
	_, _, body := request(t, srv, "GET", oldLink, nil, "")
	if !strings.Contains(string(body), "Prepared by Test author") {
		t.Fatal("default author ignored")
	}
	closeServer()
	srv, closeServer = start(t, dir)
	defer closeServer()
	_, _, body = request(t, srv, "GET", "/settings", nil, "")
	if !strings.Contains(string(body), `value="5"`) || !strings.Contains(string(body), `value="Test author"`) {
		t.Fatal("settings lost on restart")
	}
	if code, _, _ := request(t, srv, "GET", oldLink, nil, ""); code != 200 {
		t.Fatal("existing URL changed")
	}
}
func TestAuthorPhotosAreNormalizedAndPrivateSettingsStayPrivate(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app)
	defer srv.Close()
	pub := httptest.NewServer(app.PublicHandler())
	defer pub.Close()
	upload := func(data []byte) int {
		_, _, body := request(t, srv, "GET", "/settings", nil, "")
		csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(body)[1]
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		mw.WriteField("csrf", string(csrf))
		mw.WriteField("action", "photo")
		mw.WriteField("name", "Owner")
		part, _ := mw.CreateFormFile("photo", "photo.png")
		part.Write(data)
		mw.Close()
		req, _ := http.NewRequest("POST", srv.URL+"/settings", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Origin", srv.URL)
		c := *srv.Client()
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, e := c.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}
	if code := upload([]byte("<svg onload='alert(1)'/>")); code != 400 {
		t.Fatal("accepted nonphoto", code)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	img.Set(150, 100, color.NRGBA{R: 120, A: 255})
	var data bytes.Buffer
	png.Encode(&data, img)
	if code := upload(data.Bytes()); code != 303 {
		t.Fatal(code)
	}
	code, h, _ := authorForm(t, srv, "/new", url.Values{"title": {"Photo check"}, "markdown": {"Hello"}, "slug": {"photo-check"}, "access": {"public"}}, srv.URL)
	if code != 303 {
		t.Fatal(code, h)
	}
	for _, server := range []*httptest.Server{srv, pub} {
		_, headers, body := request(t, server, "GET", "/photo-check", nil, "")
		m := regexp.MustCompile(`data:image/png;base64,([A-Za-z0-9+/=]+)`).FindSubmatch(body)
		if len(m) != 2 {
			t.Fatal("avatar absent")
		}
		raw, _ := base64.StdEncoding.DecodeString(string(m[1]))
		cfg, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil || cfg.Width != 128 || cfg.Height != 128 {
			t.Fatal("photo not normalized")
		}
		if !strings.Contains(headers.Get("Content-Security-Policy"), "img-src data:") {
			t.Fatal("photo CSP")
		}
	}
	if code, _, _ := request(t, pub, "GET", "/settings", nil, ""); code != 404 {
		t.Fatal("settings public")
	}
	if code, _, _ := authorForm(t, srv, "/settings", url.Values{"action": {"remove_photo"}, "name": {"Owner"}}, srv.URL); code != 303 {
		t.Fatal(code)
	}
	_, _, body := request(t, pub, "GET", "/photo-check", nil, "")
	if strings.Contains(string(body), "data:image/png;base64,") {
		t.Fatal("removed avatar still served")
	}
}

func TestOldLinkPreferencesMigrateToThreeCharacters(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "cairn.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE preferences(id INTEGER PRIMARY KEY,author TEXT NOT NULL,template TEXT NOT NULL,link_length INTEGER NOT NULL CHECK(link_length BETWEEN 6 AND 24));INSERT INTO preferences VALUES(1,'Keep me','visual',12);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	srv, closeServer := start(t, dir)
	defer closeServer()
	_, _, body := request(t, srv, "GET", "/settings", nil, "")
	if !strings.Contains(string(body), `value="Keep me"`) || !strings.Contains(string(body), `value="3"`) || !strings.Contains(string(body), `value="visual" selected`) {
		t.Fatal("migration lost preferences")
	}
	code, h, _ := authorForm(t, srv, "/new", url.Values{"title": {"Short link"}, "markdown": {"Hello"}}, srv.URL)
	if code != 303 || !regexp.MustCompile(`^/[a-zA-Z0-9]{3}$`).MatchString(h.Get("Location")) {
		t.Fatal(code, h)
	}
	for _, length := range []string{"2", "8"} {
		if code, _, _ := authorForm(t, srv, "/settings", url.Values{"action": {"defaults"}, "author": {"Keep me"}, "template": {"visual"}, "link_length": {length}}, srv.URL); code != 400 {
			t.Fatal("accepted length", length, code)
		}
	}
}
