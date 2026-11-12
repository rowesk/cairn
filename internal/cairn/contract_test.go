package cairn_test

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

const setupMarker = "<!-- agent setup text starts here -->\n"

func TestAPIIndexMirrorsCanonicalContract(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()

	status, _, body := request(t, srv, "GET", "/api", nil, "")
	if status != 200 {
		t.Fatalf("index: %d %s", status, body)
	}
	var index struct {
		Version   string `json:"version"`
		OpenAPI   string `json:"openapi"`
		Auth      string `json:"authentication"`
		Endpoints []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"endpoints"`
		Limits     map[string]any `json:"limits"`
		ErrorCodes []string       `json:"error_codes"`
		Example    struct {
			Request struct {
				Method string         `json:"method"`
				Path   string         `json:"path"`
				Body   map[string]any `json:"body"`
			} `json:"request"`
			Response struct {
				Status int            `json:"status"`
				Body   map[string]any `json:"body"`
			} `json:"response"`
		} `json:"example"`
	}
	if err := json.Unmarshal(body, &index); err != nil {
		t.Fatal(err)
	}
	if index.Version != cairn.APIVersion || index.OpenAPI != "/api/openapi.json" || !strings.Contains(index.Auth, "Bearer") {
		t.Fatalf("index: %+v", index)
	}
	paths := map[string]bool{}
	for _, e := range index.Endpoints {
		paths[e.Method+" "+e.Path] = true
	}
	for _, want := range []string{"GET /api", "GET /api/openapi.json", "POST /api/pages", "GET /api/pages", "GET /api/pages/{slug}", "PUT /api/pages/{slug}", "PATCH /api/pages/{slug}", "GET /healthz"} {
		if !paths[want] {
			t.Errorf("index missing %s: %+v", want, index.Endpoints)
		}
	}
	for _, want := range []string{"payload_too_large", "slug_conflict", "authentication_required", "invalid_credential", "idempotency_key_reused"} {
		found := false
		for _, code := range index.ErrorCodes {
			found = found || code == want
		}
		if !found {
			t.Errorf("index missing error code %s", want)
		}
	}
	if index.Limits["request_bytes"] != float64(16<<20) || index.Limits["default_slug_chars"] != float64(3) || index.Limits["slug_chars"] != float64(3) || index.Limits["title_bytes"] != float64(300) {
		t.Errorf("limits: %v", index.Limits)
	}
	// The worked example is complete: it publishes, and its response has the
	// shape the server really sends.
	if index.Example.Request.Method != "POST" || index.Example.Response.Status != 201 || index.Example.Request.Body["markdown"] == nil {
		t.Fatalf("example: %+v", index.Example)
	}
	example := index.Example.Request.Body
	example["agent"] = "Research agent"
	if status, _, body := request(t, srv, "POST", "/api/pages", example, token); status != 201 {
		t.Fatalf("the index's example does not publish: %d %s", status, body)
	}
	doc := loadOpenAPI(t, srv)
	if problems := doc.validate("#/components/schemas/PageWrite", toAny(index.Example.Response.Body)); len(problems) > 0 {
		t.Errorf("example response does not match the document: %v", problems)
	}
}

func TestAgentSetupMatchesContract(t *testing.T) {
	contract, err := os.ReadFile("contract.md")
	if err != nil {
		t.Fatal(err)
	}
	notes, setup, found := strings.Cut(string(contract), setupMarker)
	if !found || !strings.Contains(notes, "Maintainer notes") {
		t.Fatal("contract.md has no marker between its maintainer notes and the setup text")
	}
	rendered, err := os.ReadFile("ui/agent-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != setup {
		t.Error("ui/agent-setup.md drifted from the setup text in contract.md")
	}
	for _, want := range []string{"$CAIRN_API_KEY", "CAIRN_URL", "{{ORIGIN}}", "{{AUTHOR}}", "/api/pages", "Idempotency-Key", "?asset=", "[^1]", "[!NOTE]", "mermaid", "echarts", "comparison", "visual", "validation_failed", "invalid_credential", "slug_conflict", "private_url", "dropped_html", "PATCH", "16 MiB", "40 words"} {
		if !strings.Contains(setup, want) {
			t.Errorf("setup text does not mention %q", want)
		}
	}
	prose := regexp.MustCompile("(?s)```.*?```").ReplaceAllString(setup, "")
	if words := len(strings.Fields(prose)); words > 700 {
		t.Errorf("setup text is %d words; keep it under 700", words)
	}
}

func TestRenderedSetupHoldsNoSecretOrMaintainerText(t *testing.T) {
	dir := t.TempDir()
	app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: token, Owner: "Alex Example", PrivateBaseURL: "https://cairn.example.ts.net"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app)
	defer srv.Close()
	key := newKey(t, srv, "Researcher")
	_, _, body := request(t, srv, "GET", "/settings", nil, "")
	page := string(body)
	match := regexp.MustCompile(`(?s)<textarea id="agent-setup-guide"[^>]*>(.*?)</textarea>`).FindStringSubmatch(page)
	if match == nil {
		t.Fatal("settings has no setup text")
	}
	setup := html.UnescapeString(match[1])
	for _, text := range []string{setup, page} {
		for _, secret := range []string{key, token} {
			if strings.Contains(text, secret) {
				t.Fatal("a secret reached the settings page")
			}
		}
	}
	if regexp.MustCompile(`cairn_[A-Za-z0-9_-]{20,}`).MatchString(setup) || strings.Contains(setup, "{{KEY}}") {
		t.Error("setup text has a key or a key placeholder")
	}
	for _, maintainer := range []string{"Maintainer", "contract_test", "contract.md", "ships", "Rules for editing", "placeholder", "<!--"} {
		if strings.Contains(setup, maintainer) {
			t.Errorf("setup text holds maintainer text %q", maintainer)
		}
	}
	for _, personal := range []string{"Tailscale", "Raspberry", " Pi ", "Cloudflare", "SSH", " him", " his ", "Alex Example"} {
		if strings.Contains(setup, personal) {
			t.Errorf("setup text names a deployment or a person: %q", personal)
		}
	}
	if !strings.Contains(setup, "$CAIRN_API_KEY") || !strings.Contains(page, `data-origin="https://cairn.example.ts.net"`) {
		t.Error("setup text should read the key from CAIRN_API_KEY and use the private base URL")
	}
}

// openAPI is a small JSON Schema reader for the served document: enough to
// prove real responses have the documented shape.
type openAPI struct{ doc map[string]any }

func loadOpenAPI(t *testing.T, srv *httptest.Server) openAPI {
	t.Helper()
	status, header, body := request(t, srv, "GET", "/api/openapi.json", nil, "")
	if status != 200 || header.Get("Content-Type") != "application/json" {
		t.Fatalf("openapi: %d", status)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi version: %v", doc["openapi"])
	}
	return openAPI{doc}
}

func toAny(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	json.Unmarshal(raw, &out)
	return out
}

func (o openAPI) resolve(schema map[string]any) map[string]any {
	for {
		ref, ok := schema["$ref"].(string)
		if !ok {
			return schema
		}
		node := any(o.doc)
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			node = node.(map[string]any)[part]
		}
		schema = node.(map[string]any)
	}
}

func jsonType(v any) string {
	switch n := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case float64:
		if n == float64(int64(n)) {
			return "integer"
		}
		return "number"
	}
	return "unknown"
}

func (o openAPI) validate(where string, v any) []string {
	schema := o.resolve(map[string]any{"$ref": where})
	return o.check("$", schema, v)
}

func (o openAPI) check(path string, schema map[string]any, v any) []string {
	schema = o.resolve(schema)
	var problems []string
	if anyOf, ok := schema["anyOf"].([]any); ok {
		for _, option := range anyOf {
			if len(o.check(path, option.(map[string]any), v)) == 0 {
				return nil
			}
		}
		return []string{path + ": matches no anyOf option"}
	}
	if types, ok := schema["type"]; ok {
		allowed := []string{}
		switch t := types.(type) {
		case string:
			allowed = append(allowed, t)
		case []any:
			for _, name := range t {
				allowed = append(allowed, name.(string))
			}
		}
		got := jsonType(v)
		fits := false
		for _, name := range allowed {
			fits = fits || name == got || (name == "number" && got == "integer")
		}
		if !fits {
			return []string{fmt.Sprintf("%s: %s is not %v", path, got, allowed)}
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, value := range enum {
			found = found || value == v
		}
		if !found {
			problems = append(problems, fmt.Sprintf("%s: %v is not one of %v", path, v, enum))
		}
	}
	switch value := v.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range toStrings(schema["required"]) {
			if _, ok := value[name]; !ok {
				problems = append(problems, path+"."+name+": required but missing")
			}
		}
		for name, item := range value {
			if property, ok := properties[name]; ok {
				problems = append(problems, o.check(path+"."+name, property.(map[string]any), item)...)
				continue
			}
			switch extra := schema["additionalProperties"].(type) {
			case bool:
				if !extra {
					problems = append(problems, path+"."+name+": not in the document")
				}
			case map[string]any:
				problems = append(problems, o.check(path+"."+name, extra, item)...)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range value {
				problems = append(problems, o.check(path+"["+strconv.Itoa(i)+"]", items, item)...)
			}
		}
		if n, ok := schema["minItems"].(float64); ok && len(value) < int(n) {
			problems = append(problems, path+": too few items")
		}
		if n, ok := schema["maxItems"].(float64); ok && len(value) > int(n) {
			problems = append(problems, path+": too many items")
		}
	}
	return problems
}

func toStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.(string))
	}
	return out
}

// operation finds the documented response for a real request.
func (o openAPI) response(method, path string, status int) (map[string]any, bool) {
	template := path
	if strings.HasPrefix(path, "/api/pages/") {
		template = "/api/pages/{slug}"
	}
	item, ok := o.doc["paths"].(map[string]any)[template].(map[string]any)
	if !ok {
		return nil, false
	}
	op, ok := item[strings.ToLower(method)].(map[string]any)
	if !ok {
		return nil, false
	}
	r, ok := op["responses"].(map[string]any)[strconv.Itoa(status)].(map[string]any)
	return r, ok
}

func TestOpenAPIDescribesTheHandlersRealBehaviour(t *testing.T) {
	a := startAPI(t, "https://cairn.example.ts.net")
	doc := loadOpenAPI(t, a.private)
	key, other := newKey(t, a.private, "Researcher"), newKey(t, a.private, "Planner")
	call(t, a.private, "POST", "/api/pages", map[string]any{"slug": "theirs", "title": "Theirs", "original_request": "SECRET-THEIRS", "markdown": "x"}, bearer(other))
	cases := []struct {
		method, path string
		body         any
		headers      map[string]string
		status       int
	}{
		{"POST", "/api/pages", map[string]any{"slug": "lamps", "title": "Lamps", "original_request": "r", "markdown": "Intro <script>x</script>\n\n![x](?asset=x.png)", "assets": map[string]string{"x.png": pngBase64}}, bearer(key), 201},
		{"POST", "/api/pages", map[string]any{"title": "Custom", "original_request": "r", "html": "<p>x</p>", "template": "visual"}, bearer(key), 201},
		{"POST", "/api/pages", map[string]any{"title": "", "titel": 4, "template": "x", "assets": map[string]any{"a.png": 3}}, bearer(key), 400},
		{"POST", "/api/pages", `{"title":`, bearer(key), 400},
		{"POST", "/api/pages", map[string]any{"slug": "lamps", "title": "Lamps", "original_request": "r", "markdown": "x"}, bearer(key), 409},
		{"POST", "/api/pages", map[string]any{"title": "Lamps"}, nil, 401},
		{"POST", "/api/pages", map[string]any{"title": "Lamps"}, bearer("cairn_nope"), 401},
		{"POST", "/api/pages", map[string]any{"title": "Lamps"}, map[string]string{"Authorization": "Bearer " + key, "Origin": "https://evil.example"}, 403},
		{"POST", "/api/pages", "x", map[string]string{"Authorization": "Bearer " + key, "Content-Type": "text/plain"}, 415},
		{"POST", "/api/pages", map[string]any{"title": "Lamps", "original_request": "r", "markdown": "x"}, map[string]string{"Authorization": "Bearer " + key, "Idempotency-Key": "a b"}, 400},
		{"GET", "/api/pages", nil, bearer(key), 200},
		{"GET", "/api/pages?q=lamps", nil, bearer(key), 200},
		{"GET", "/api/pages?limit=1", nil, bearer(key), 200},
		{"GET", "/api/pages?limit=x", nil, bearer(key), 400},
		{"GET", "/api/pages?agent=Planner", nil, bearer(key), 403},
		{"GET", "/api/pages/lamps", nil, bearer(key), 200},
		{"GET", "/api/pages/theirs", nil, bearer(key), 404},
		{"GET", "/api/pages/nothing", nil, bearer(key), 404},
		{"PUT", "/api/pages/lamps", map[string]any{"title": "Lamps", "original_request": "r", "markdown": "y"}, bearer(key), 200},
		{"PUT", "/api/pages/theirs", map[string]any{"title": "Lamps", "original_request": "r", "markdown": "y"}, bearer(key), 404},
		{"PUT", "/api/pages/nothing", map[string]any{"title": "Lamps", "original_request": "r", "markdown": "y"}, bearer(key), 404},
		{"PATCH", "/api/pages/lamps", map[string]any{"archived": true}, bearer(key), 200},
		{"PATCH", "/api/pages/lamps", map[string]any{"archived": "yes"}, bearer(key), 400},
		{"GET", "/healthz", nil, nil, 200},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		headers := tc.headers
		status, _, _, raw := call(t, a.private, tc.method, tc.path, tc.body, headers)
		if status != tc.status {
			t.Errorf("%s %s: got %d, want %d: %s", tc.method, tc.path, status, tc.status, raw)
			continue
		}
		route, _, _ := strings.Cut(tc.path, "?")
		documented, ok := doc.response(tc.method, route, status)
		if !ok {
			t.Errorf("%s %s answers %d, which the document does not list", tc.method, route, status)
			continue
		}
		seen[tc.method+" "+route+" "+strconv.Itoa(status)] = true
		content, _ := documented["content"].(map[string]any)
		schema := content["application/json"].(map[string]any)["schema"].(map[string]any)
		var body any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("%s %s: not JSON", tc.method, tc.path)
		}
		if problems := doc.check("$", schema, body); len(problems) > 0 {
			t.Errorf("%s %s %d does not match the document: %v\n%s", tc.method, tc.path, status, problems, raw)
		}
	}
	if len(seen) < 15 {
		t.Errorf("only %d operations checked", len(seen))
	}
	// Every documented operation answers on the private listener.
	for path, item := range doc.doc["paths"].(map[string]any) {
		for method := range item.(map[string]any) {
			if method == "parameters" {
				continue
			}
			concrete := strings.ReplaceAll(path, "{slug}", "lamps")
			status, _, _, _ := call(t, a.private, strings.ToUpper(method), concrete, map[string]any{"archived": false}, bearer(key))
			if status == 404 && !strings.Contains(path, "{slug}") || status == 405 {
				t.Errorf("documented %s %s answers %d", method, path, status)
			}
		}
	}
	// Request schemas carry the same limits the validator uses.
	publication := doc.resolve(map[string]any{"$ref": "#/components/schemas/Publication"})["properties"].(map[string]any)
	if publication["title"].(map[string]any)["x-max-bytes"] != float64(300) || publication["request_summary"].(map[string]any)["x-max-words"] != float64(40) || fmt.Sprint(publication["template"].(map[string]any)["enum"]) != "[report comparison visual]" {
		t.Errorf("publication schema: %v", publication)
	}
	var fields []string
	for name := range publication {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	if strings.Join(fields, " ") != "agent assets capabilities html markdown original_request request_source request_summary slug template title" {
		t.Errorf("publication fields: %v", fields)
	}
	// The document reveals nothing private.
	_, _, raw := request(t, a.private, "GET", "/api/openapi.json", nil, "")
	u, _ := url.Parse(a.private.URL)
	for _, private := range []string{token, key, "SECRET-THEIRS", "Lamps", "Researcher", "Planner", u.Host, "cairn.example.ts.net\"", "reports.example.com", "Owner"} {
		if strings.Contains(string(raw), private) {
			t.Errorf("the OpenAPI document holds %q", private)
		}
	}
	if _, ok := doc.doc["servers"]; ok {
		t.Error("the OpenAPI document names a server")
	}
}

func TestPublicListener404sAllNewRoutes(t *testing.T) {
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	private := httptest.NewServer(app)
	defer private.Close()
	public := httptest.NewServer(app.PublicHandler())
	defer public.Close()

	in := page("Shared")
	if status, _, body := request(t, private, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("create: %d %s", status, body)
	}
	if code, _, _ := authorForm(t, private, "/manage/report", url.Values{"action": {"share"}}, private.URL); code != 303 {
		t.Fatal("share", code)
	}
	for _, path := range []string{
		"/api", "/api/openapi.json", "/api/pages", "/api/pages?q=Shared", "/api/pages/report",
		"/settings", "/new", "/import", "/manage/report", "/manage/report/delete",
		"/", "/healthz/", "/_library/request/report",
	} {
		if status, _, _ := request(t, public, "GET", path, nil, token); status != 404 {
			t.Errorf("public %s got %d, want 404", path, status)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if status, _, _ := request(t, public, method, "/api/pages/report", page("x"), token); status != 404 {
			t.Errorf("public %s /api/pages/report got %d, want 404", method, status)
		}
	}
	if status, _, body := request(t, public, "POST", "/api/pages", page("x"), token); status != 404 {
		t.Errorf("public POST /api/pages got %d, want 404: %s", status, body)
	}
	if status, _, body := request(t, public, "GET", "/healthz", nil, ""); status != 200 || strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Errorf("public health: %d %s", status, body)
	}
}
