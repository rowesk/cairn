package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rowesk/cairn/internal/cairn"
)

const onePixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="

// library runs a real Cairn on a loopback port and returns its address and
// an agent key created through Settings, as the owner would.
func library(t *testing.T, private string) (string, string) {
	t.Helper()
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: testToken, PrivateBaseURL: private})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(func() { srv.Close(); app.Close() })
	res, err := http.Get(srv.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)[1]
	form := url.Values{"csrf": {string(csrf)}, "action": {"key_create"}, "name": {"Researcher"}}
	req, _ := http.NewRequest("POST", srv.URL+"/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var created struct{ Key string }
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil || created.Key == "" {
		t.Fatalf("key: %v", err)
	}
	return srv.URL, created.Key
}

func runPublish(t *testing.T, env map[string]string, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	c := &cli{prog: "cairn", stdin: strings.NewReader(stdin), stdout: &stdout, stderr: &stderr, getenv: mapEnv(env)}
	code := c.run(context.Background(), append([]string{"publish"}, args...))
	return result{code, stdout.String(), stderr.String()}
}

func writeFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func apiGet(t *testing.T, base, key, path string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return out
}

func TestPublishPrintsTheFullAddress(t *testing.T) {
	base, key := library(t, "")
	env := map[string]string{"CAIRN_URL": base + "/", "CAIRN_API_KEY": key}
	dir := t.TempDir()
	image, _ := base64.StdEncoding.DecodeString(onePixel)
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), image, 0600); err != nil {
		t.Fatal(err)
	}
	file := writeFile(t, dir, "lamps.md", "# Desk lamps under £100\n\nThree lamps.\n\n![Chart](?asset=chart.png)\n")
	got := runPublish(t, env, "", file, "-request", "find me a lamp", "--summary", "Find a lamp.", "-template", "comparison")
	if got.code != 0 || got.stderr != "" || !regexp.MustCompile(`^`+regexp.QuoteMeta(base)+`/[A-Za-z0-9]{3}\n$`).MatchString(got.stdout) {
		t.Fatalf("code %d\nstdout %q\nstderr %q", got.code, got.stdout, got.stderr)
	}
	slug := strings.TrimSpace(strings.TrimPrefix(got.stdout, base+"/"))
	page := apiGet(t, base, key, "/api/pages/"+slug)
	if page["title"] != "Desk lamps under £100" || page["template"] != "comparison" || page["request_summary"] != "Find a lamp." || page["agent"] != "Researcher" || len(page["assets"].([]any)) != 1 {
		t.Fatalf("saved page: %v", page)
	}
	// Flags may come before the file; -json prints Cairn's reply.
	got = runPublish(t, env, "", "-json", "-title", "Replaced", "-request", "again", "-replace", slug, file)
	var reply map[string]any
	if got.code != 0 || json.Unmarshal([]byte(got.stdout), &reply) != nil || reply["slug"] != slug || reply["title"] != "Replaced" {
		t.Fatalf("replace: %d %s %s", got.code, got.stdout, got.stderr)
	}
	// Markdown from standard input, and the private base URL when set.
	base2, key2 := library(t, "https://cairn.example.ts.net")
	got = runPublish(t, map[string]string{"CAIRN_API_KEY": key2}, "Piped words.\n", "-", "-url", base2, "-title", "Piped", "-request", "save this")
	if got.code != 0 || !regexp.MustCompile(`^https://cairn\.example\.ts\.net/[A-Za-z0-9]{3}\n$`).MatchString(got.stdout) {
		t.Fatalf("stdin: %d %q %q", got.code, got.stdout, got.stderr)
	}
	// What Cairn removes is reported on standard error.
	html := writeFile(t, dir, "custom.md", "Hello <script>x()</script>")
	got = runPublish(t, env, "", html, "-title", "Scripted", "-request", "r")
	if got.code != 0 || !strings.Contains(got.stderr, "Cairn removed raw HTML it does not allow from the Markdown: elements script.") {
		t.Fatalf("warning: %d %q", got.code, got.stderr)
	}
}

func TestPublishFailuresNameTheFlags(t *testing.T) {
	base, key := library(t, "")
	env := map[string]string{"CAIRN_URL": base, "CAIRN_API_KEY": key}
	dir := t.TempDir()
	file := writeFile(t, dir, "notes.md", "No heading here.")
	got := runPublish(t, env, "", file, "-template", "fancy", "-slug", "API")
	want := "Cairn did not publish " + file + ":\n" +
		"  -title: title is required. Give the page a short, specific title.\n" +
		"  -request: original_request is required. Send the owner's request in their own words.\n" +
		"  -template: template \"fancy\" is not one of report, comparison or visual.\n" +
		"  -slug: slug \"API\" is a name Cairn uses itself. Choose another, or leave slug out.\n" +
		"Fix these and run the command again.\n"
	if got.code != 1 || got.stderr != want || got.stdout != "" {
		t.Fatalf("code %d\nstderr:\n%s\nwant:\n%s", got.code, got.stderr, want)
	}
	got = runPublish(t, env, "", "-json", file)
	if got.code != 1 || !strings.Contains(got.stdout, `"code":"validation_failed"`) || got.stderr != "" {
		t.Fatalf("json failure: %d %q %q", got.code, got.stdout, got.stderr)
	}
	got = runPublish(t, map[string]string{"CAIRN_URL": base, "CAIRN_API_KEY": "cairn_revoked"}, "", file, "-title", "x", "-request", "y")
	if got.code != 1 || got.stderr != "Cairn did not accept the key in CAIRN_API_KEY. It may have been rotated or revoked.\nCreate a new key in Settings, Authors, then set CAIRN_API_KEY to it.\n" {
		t.Fatalf("bad key: %d %q", got.code, got.stderr)
	}
	got = runPublish(t, env, "", file, "-title", "x", "-request", "y", "-replace", "nothing")
	if got.code != 1 || got.stderr != "No page has the slug nothing.\nCheck -replace, or leave it out to publish a new page.\n" {
		t.Fatalf("missing page: %d %q", got.code, got.stderr)
	}
	runPublish(t, env, "", file, "-title", "x", "-request", "y", "-slug", "taken")
	got = runPublish(t, env, "", file, "-title", "x", "-request", "y", "-slug", "Taken")
	if got.code != 1 || !strings.HasPrefix(got.stderr, "Cairn did not publish "+file+":\n  -slug: slug \"Taken\" is taken. Slugs that differ only in letter case count as the same.") || strings.Contains(got.stderr, "\"taken\"") {
		t.Fatalf("taken: %d %q", got.code, got.stderr)
	}
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	address := closed.Addr().String()
	closed.Close()
	got = runPublish(t, map[string]string{"CAIRN_URL": "http://" + address, "CAIRN_API_KEY": key}, "", file, "-title", "x", "-request", "y")
	if got.code != 1 || got.stderr != "Cairn could not reach http://"+address+": nothing is listening there.\nCheck CAIRN_URL, and that Cairn is running.\n" {
		t.Fatalf("unreachable: %d %q", got.code, got.stderr)
	}
	if strings.Contains(got.stderr+got.stdout, key) {
		t.Fatal("the key was printed")
	}
}

func TestPublishBadInvocations(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "notes.md", "# Notes")
	text := writeFile(t, dir, "notes.txt", "Notes")
	env := map[string]string{"CAIRN_URL": "http://127.0.0.1:9", "CAIRN_API_KEY": "cairn_x"}
	cases := []struct {
		name   string
		env    map[string]string
		args   []string
		code   int
		stderr string
	}{
		{"no file", env, nil, 2, "Name the file to publish, such as:\n  cairn publish notes.md -request \"...\"\nRun cairn help publish to see the flags.\n"},
		{"two files", env, []string{file, file}, 2, "Unexpected argument \"" + file + "\". publish takes one file.\nA value with spaces needs quotes.\nRun cairn help publish to see the flags.\n"},
		{"unquoted request", env, []string{file, "-request", "find", "me"}, 2, "Unexpected argument \"me\". publish takes one file.\nA value with spaces needs quotes.\nRun cairn help publish to see the flags.\n"},
		{"flag typo", env, []string{file, "-titel", "x"}, 2, "Unknown flag -titel. Did you mean -title?\nRun cairn help publish to see the flags.\n"},
		{"key as a flag", env, []string{file, "-key", "cairn_x"}, 2, "Unknown flag -key. Put the key in CAIRN_API_KEY instead, so it never shows\nin the process list.\n"},
		{"flag without value", env, []string{file, "-title"}, 2, "-title needs a value, in the form -title TEXT.\nRun cairn help publish to see the flags.\n"},
		{"slug and replace", env, []string{file, "-slug", "a", "-replace", "b"}, 2, "-slug a and -replace b name different pages.\nA replaced page keeps its slug, so leave -slug out.\n"},
		{"not markdown or html", env, []string{text}, 2, "Cairn publishes Markdown (.md) or HTML (.html) files, and " + text + " is neither.\nRename it, or pipe Markdown in with: cairn publish - < " + text + "\n"},
		{"no address", map[string]string{"CAIRN_API_KEY": "cairn_x"}, []string{file}, 1, "Cairn does not know where your library is.\nSet CAIRN_URL to its address, such as http://127.0.0.1:8080, or pass -url.\n"},
		{"no key", map[string]string{"CAIRN_URL": "http://127.0.0.1:9"}, []string{file}, 1, "CAIRN_API_KEY is not set.\nCreate an agent key in Cairn under Settings, Authors, then set\nCAIRN_API_KEY to it in this environment.\n"},
		{"bad address", map[string]string{"CAIRN_URL": "cairn.local", "CAIRN_API_KEY": "cairn_x"}, []string{file}, 1, "CAIRN_URL must be the library's address, such as http://127.0.0.1:8080.\nIt is set to \"cairn.local\".\n"},
		{"missing file", env, []string{filepath.Join(dir, "missing.md")}, 1, "The file " + filepath.Join(dir, "missing.md") + " does not exist.\n"},
		{"missing image", env, []string{file, "-asset", filepath.Join(dir, "missing.png")}, 1, "The image " + filepath.Join(dir, "missing.png") + " does not exist.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runPublish(t, tc.env, "", tc.args...)
			if got.code != tc.code || got.stderr != tc.stderr || got.stdout != "" {
				t.Fatalf("code %d, want %d\nstderr:\n%s\nwant:\n%s", got.code, tc.code, got.stderr, tc.stderr)
			}
		})
	}
	if got := runCLI(t, nil, "post"); got.code != 2 || got.stderr != "Unknown command \"post\". Did you mean \"publish\"?\nRun cairn help to see the commands.\n" {
		t.Fatalf("guess: %q", got.stderr)
	}
}

func TestPublishRetriesWithOneIdempotencyKey(t *testing.T) {
	saved := publishRetryWait
	publishRetryWait = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { publishRetryWait = saved })
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: testToken})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var mu sync.Mutex
	var keys []string
	failures := 2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		fail := failures > 0
		failures--
		mu.Unlock()
		if fail {
			// The first attempt is saved but its answer is lost; the second
			// meets a busy server.
			if len(keys) == 1 {
				app.ServeHTTP(httptest.NewRecorder(), r)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"code":"storage_unavailable","message":"busy","retryable":true}`)
			return
		}
		app.ServeHTTP(w, r)
	}))
	defer srv.Close()
	file := writeFile(t, t.TempDir(), "notes.md", "# Notes\n\nWords.")
	got := runPublish(t, map[string]string{"CAIRN_URL": srv.URL, "CAIRN_API_KEY": testToken}, "", file, "-request", "r", "-agent", "Researcher")
	if got.code != 0 {
		t.Fatalf("%d %q", got.code, got.stderr)
	}
	if len(keys) != 3 || keys[0] == "" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("keys: %q", keys)
	}
	list := apiGet(t, srv.URL, testToken, "/api/pages")
	if list["total"] != float64(1) {
		t.Fatalf("retries saved %v pages", list["total"])
	}
}
