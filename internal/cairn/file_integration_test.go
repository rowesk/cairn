package cairn_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFilePageUsesLibraryAPIAndCannotBeEdited(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	const original = "# Keep this as a file"
	c, b := publishFilePage(t, srv, "POST", "/api/files", "unique-attachment.md", []byte(original), [2]string{"title", "Stored attachment"}, [2]string{"slug", "attachment"})
	if c != 201 {
		t.Fatalf("create: %d %s", c, b)
	}
	c, _, b = request(t, srv, "GET", "/api/pages/attachment", nil, token)
	var p struct {
		Format   string
		Editable bool
		Source   struct {
			Name        string
			Size        int64
			Text        *string
			DownloadURL string `json:"download_url"`
		}
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if c != 200 || p.Format != "file" || p.Editable || p.Source.Name != "unique-attachment.md" || p.Source.Size != int64(len(original)) || p.Source.Text != nil || p.Source.DownloadURL != "/attachment?download=1" {
		t.Fatalf("metadata: %d %s", c, b)
	}
	for _, v := range []any{true, false, nil} {
		c, _, b = request(t, srv, "PATCH", "/api/pages/attachment", map[string]any{"title": "Changed", "share_enabled": v}, token)
		if c != 400 || !strings.Contains(string(b), `"field":"share_enabled"`) {
			t.Fatalf("PATCH sharing: %d %s", c, b)
		}
	}
	for _, q := range []string{"Stored", "unique"} {
		c, _, b = request(t, srv, "GET", "/api/pages?q="+q, nil, token)
		if c != 200 || !strings.Contains(string(b), `"slug":"attachment"`) {
			t.Fatalf("search %s: %d %s", q, c, b)
		}
	}
	c, _, b = request(t, srv, "GET", "/edit/attachment", nil, "")
	if c == http.StatusOK || strings.Contains(string(b), `id="markdown"`) {
		t.Fatalf("file was editable: %d", c)
	}
	if c, _, b = request(t, srv, "GET", "/attachment?download=1", nil, ""); c != 200 || string(b) != original {
		t.Fatalf("download: %d %s", c, b)
	}
}
