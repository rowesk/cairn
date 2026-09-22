package cairn_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

func TestAPIIndexMirrorsCanonicalContract(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()

	status, _, body := request(t, srv, "GET", "/api", nil, "")
	if status != 200 {
		t.Fatalf("index: %d %s", status, body)
	}
	var index struct {
		Version   string `json:"version"`
		Endpoints []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"endpoints"`
		Limits     map[string]any `json:"limits"`
		ErrorCodes []string       `json:"error_codes"`
	}
	if err := json.Unmarshal(body, &index); err != nil {
		t.Fatal(err)
	}
	if index.Version != cairn.APIVersion || index.Version == "" {
		t.Fatalf("version: %+v", index)
	}
	paths := map[string]bool{}
	for _, e := range index.Endpoints {
		paths[e.Method+" "+e.Path] = true
	}
	for _, want := range []string{"GET /api", "POST /api/pages", "PUT /api/pages/{slug}"} {
		if !paths[want] {
			t.Errorf("index missing %s: %+v", want, index.Endpoints)
		}
	}
	for _, want := range []string{"payload_too_large", "slug_conflict", "authentication_required"} {
		found := false
		for _, code := range index.ErrorCodes {
			found = found || code == want
		}
		if !found {
			t.Errorf("index missing error code %s", want)
		}
	}
	if index.Limits["request_bytes"] != float64(16<<20) || index.Limits["default_slug_chars"] != float64(3) || index.Limits["slug_chars"] != float64(3) {
		t.Errorf("limits: %v", index.Limits)
	}

	contract, err := os.ReadFile("contract.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/_content/{slug}", "?asset=", "payload_too_large", "GET /api", "16"} {
		if !strings.Contains(string(contract), want) {
			t.Errorf("contract.md missing %q", want)
		}
	}
	rendered, err := os.ReadFile("ui/agent-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != string(contract) {
		t.Error("ui/agent-setup.md drifted from contract.md")
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
	for _, path := range []string{
		"/api", "/api/pages", "/api/pages/report",
		"/settings", "/new", "/import", "/manage/report", "/manage/report/delete",
		"/", "/report",
	} {
		if status, _, _ := request(t, public, "GET", path, nil, ""); status != 404 {
			t.Errorf("public %s got %d, want 404", path, status)
		}
	}
	if status, _, _ := request(t, public, "POST", "/api/pages", page("x"), token); status != 404 {
		t.Errorf("public POST /api/pages got %d, want 404", status)
	}
}
