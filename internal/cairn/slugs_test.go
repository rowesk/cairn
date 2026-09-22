package cairn_test

import (
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

func publicPath(t *testing.T, srv *httptest.Server, slug string) string {
	t.Helper()
	_, _, body := request(t, srv, "GET", "/manage/"+slug, nil, "")
	m := regexp.MustCompile(`name="public_slug" value="([^"]+)"`).FindSubmatch([]byte(body))
	if len(m) != 2 {
		t.Fatalf("missing public path field: %s", body)
	}
	return string(m[1])
}

func TestSamePathSharingLifecycle(t *testing.T) {
	dir := t.TempDir()
	app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	private := httptest.NewServer(app)
	public := httptest.NewServer(app.PublicHandler())
	defer func() { private.Close(); public.Close(); app.Close() }()

	// Cover API creation and the editor's immediate Save & share path.
	for _, immediate := range []bool{false, true} {
		var route string
		if immediate {
			code, headers, body := authorForm(t, private, "/new", url.Values{"title": {"Shared immediately"}, "markdown": {"Hello"}, "access": {"public"}}, private.URL)
			if code != 303 {
				t.Fatalf("editor: %d %s", code, body)
			}
			route = headers.Get("Location")
		} else {
			in := page("API private")
			delete(in, "slug")
			in["assets"] = map[string]string{"image.png": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="}
			status, headers, body := request(t, private, "POST", "/api/pages", in, token)
			if status != 201 {
				t.Fatalf("create: %d %s", status, body)
			}
			route = headers.Get("Location")
			if status, _, _ := request(t, public, "GET", route, nil, ""); status != 404 {
				t.Fatal("new API page is public")
			}
		}
		if !regexp.MustCompile(`^/[A-Za-z0-9]{3}$`).MatchString(route) {
			t.Fatalf("default path: %s", route)
		}
		slug := strings.TrimPrefix(route, "/")
		if !immediate {
			if got := ownerPost(t, private, slug, url.Values{"action": {"share"}}); got != 303 {
				t.Fatal(got)
			}
		}
		if got := publicPath(t, private, slug); got != slug {
			t.Fatalf("share changed path: %s != %s", got, slug)
		}
		routes := []string{route, "/_content" + route}
		if immediate {
			routes = append(routes, route+"?download=1")
		} else {
			routes = append(routes, "/_content"+route+"?asset=image.png")
		}
		for _, path := range routes {
			if status, _, _ := request(t, public, "GET", path, nil, ""); status != 200 {
				t.Fatalf("shared %s: %d", path, status)
			}
		}
		// Same path and state survive reopening the saved database.
		private.Close()
		public.Close()
		app.Close()
		app, err = cairn.Open(cairn.Config{DataDir: dir, PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		private = httptest.NewServer(app)
		public = httptest.NewServer(app.PublicHandler())
		if status, _, _ := request(t, public, "GET", route, nil, ""); status != 200 {
			t.Fatal("shared route lost on restart")
		}
		if got := ownerPost(t, private, slug, url.Values{"action": {"unshare"}}); got != 303 {
			t.Fatal(got)
		}
		for _, path := range routes {
			if status, _, _ := request(t, public, "GET", path, nil, ""); status != 404 {
				t.Fatalf("revoked %s: %d", path, status)
			}
			if status, _, _ := request(t, private, "GET", path, nil, ""); status != 200 {
				t.Fatalf("private %s: %d", path, status)
			}
		}
		if got := ownerPost(t, private, slug, url.Values{"action": {"share"}}); got != 303 {
			t.Fatal(got)
		}
		if got := publicPath(t, private, slug); got != slug {
			t.Fatal("resharing changed path")
		}
	}
}

func TestCustomAndExistingShortPublicPathsPreserved(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	private := httptest.NewServer(app)
	defer private.Close()
	public := httptest.NewServer(app.PublicHandler())
	defer public.Close()

	in := page("Custom path")
	if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("create: %d %s", status, body)
	}
	if got := ownerPost(t, private, "report", url.Values{"action": {"share"}, "public_slug": {"bulbs"}}); got != 303 {
		t.Fatal(got)
	}
	if got := publicPath(t, private, "report"); got != "bulbs" {
		t.Fatalf("custom path not preserved: %q", got)
	}
	if status, _, _ := request(t, public, "GET", "/bulbs", nil, ""); status != 200 {
		t.Fatal(status)
	}
	if status, _, _ := request(t, public, "GET", "/report", nil, ""); status != 200 {
		t.Fatal("canonical path missing beside custom alias")
	}
	if got := ownerPost(t, private, "report", url.Values{"action": {"unshare"}}); got != 303 {
		t.Fatal(got)
	}
	for _, route := range []string{"/report", "/bulbs", "/_content/report", "/_content/bulbs"} {
		if status, _, _ := request(t, public, "GET", route, nil, ""); status != 404 {
			t.Fatalf("revoked alias route %s: %d", route, status)
		}
	}
	if status, _, _ := request(t, private, "GET", "/report", nil, ""); status != 200 {
		t.Fatal("alias revocation removed private page")
	}
	if got := ownerPost(t, private, "report", url.Values{"action": {"share"}}); got != 303 {
		t.Fatal(got)
	}
	if publicPath(t, private, "report") != "bulbs" {
		t.Fatal("resharing lost existing alias")
	}
	// Legacy short links keep resolving.
	in = page("Legacy short")
	in["slug"] = "legacy"
	if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("create legacy: %d %s", status, body)
	}
	if got := ownerPost(t, private, "legacy", url.Values{"action": {"share"}, "public_slug": {"abc"}}); got != 303 {
		t.Fatal(got)
	}
	if status, _, _ := request(t, public, "GET", "/abc", nil, ""); status != 200 {
		t.Fatal("existing short link broke")
	}
	// A taken public path is rejected.
	in = page("Collision")
	in["slug"] = "collision"
	if status, _, _ := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatal(status)
	}
	if got := ownerPost(t, private, "collision", url.Values{"action": {"share"}, "public_slug": {"bulbs"}}); got != 409 {
		t.Fatalf("collision got %d, want 409", got)
	}
}

func TestPrivateAndPublicPathsCannotCollide(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app)
	defer srv.Close()
	for _, slug := range []string{"A1z", "B2y"} {
		in := page(slug)
		in["slug"] = slug
		if status, _, _ := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
			t.Fatal(status)
		}
	}
	if code := ownerPost(t, srv, "A1z", url.Values{"action": {"share"}, "public_slug": {"B2y"}}); code != 409 {
		t.Fatalf("claimed another private path: %d", code)
	}
	if code := ownerPost(t, srv, "A1z", url.Values{"action": {"share"}, "public_slug": {"C3x"}}); code != 303 {
		t.Fatal(code)
	}
	for _, action := range []string{"share", "unshare"} {
		if code := ownerPost(t, srv, "A1z", url.Values{"action": {action}, "public_slug": {"C3x"}}); code != 303 {
			t.Fatal(code)
		}
		in := page("collision")
		in["slug"] = "C3x"
		if code, _, _ := request(t, srv, "POST", "/api/pages", in, token); code != 409 {
			t.Fatalf("claimed reserved alias: %d", code)
		}
	}
}

func TestPagePathMayBeginWithAPI(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("API guide")
	in["slug"] = "api-guide"
	if code, _, _ := request(t, srv, "POST", "/api/pages", in, token); code != 201 {
		t.Fatal(code)
	}
	if code, _, _ := request(t, srv, "GET", "/api-guide", nil, ""); code != 200 {
		t.Fatalf("page mistaken for API route: %d", code)
	}
}
