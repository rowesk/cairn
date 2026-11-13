package cairn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zeroStream struct{}

func (zeroStream) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestUploadBoundaryWithoutDiskPayload(t *testing.T) {
	if maxFileBytes != 512<<20 {
		t.Fatal(maxFileBytes)
	}
	for _, n := range []int64{maxFileBytes, maxFileBytes + 1} {
		size, err := copyFileUpload(io.Discard, io.LimitReader(zeroStream{}, n))
		if size != n || errors.Is(err, errUploadTooLarge) != (n > maxFileBytes) {
			t.Fatal(size, err)
		}
	}
}

func TestKindMigrationBackfillAndRepeat(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "old.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE pages(id TEXT,object TEXT,file_page INTEGER,file_name TEXT,file_size INTEGER,share_enabled INTEGER); INSERT INTO pages VALUES('page','page',0,'',0,0),('image','image',0,'',0,1),('file','file',1,'',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, name string }{{"page", "notes.md"}, {"image", "PHOTO.PNG"}, {"file", "bundle.zip"}} {
		root := filepath.Join(dir, "objects", item.id)
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "source.json"), fmt.Appendf(nil, `{"name":%q}`, item.name), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "source.bin"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := migrateKinds(db, dir); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"page", "image", "file"} {
			var kind string
			var size, shared int
			if err := db.QueryRow(`SELECT kind,file_size,share_enabled FROM pages WHERE id=?`, id).Scan(&kind, &size, &shared); err != nil {
				t.Fatal(err)
			}
			want := "file"
			if id == "page" {
				want = "page"
			}
			if kind != want || (id != "page" && size != 7) || (shared == 1) != (id == "image") {
				t.Fatal(id, kind, size, shared)
			}
		}
	}
}

func TestStorageAndShortLinkClicks(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/links", map[string]any{"url": "https://example.org/target", "slug": "clicks", "share_enabled": true})
	for _, method := range []string{"HEAD", "GET", "GET"} {
		code, _, body := a.do(t, method, "public:/clicks", nil, nil)
		if code != 302 || body != "" {
			t.Fatal(code, body)
		}
	}
	a.s.clickWrites.Wait()
	var clicks int
	if err := a.s.db.QueryRow(`SELECT clicks FROM pages WHERE slug='clicks'`).Scan(&clicks); err != nil || clicks != 2 {
		t.Fatal(clicks, err)
	}
	a.publish(t, "POST", "/api/pages", fxPage("stored", "Stored", "Private", "Some page bytes"))
	object := filepath.Join(a.dir, "objects", "storage-fixture")
	if err := os.MkdirAll(object, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(object, "source.bin"), make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.s.db.Exec(`UPDATE pages SET kind='file',file_size=4096,file_name='data.zip',object='storage-fixture',archived=1 WHERE slug='stored'`); err != nil {
		t.Fatal(err)
	}
	a.s.waitIndexed()
	usage := a.s.measureStorage(context.Background())
	if usage.Incomplete || usage.Files != 4096 || usage.Total != usage.Pages+usage.Files+usage.Other || usage.Other <= 0 || len(usage.Largest) != 1 || usage.Largest[0].Slug != "stored" {
		t.Fatalf("%+v", usage)
	}
	if usage.DiskKnown && (usage.Free <= 0 || usage.Capacity < usage.Free) {
		t.Fatal(usage)
	}
	if code, _, _ := a.form(t, "/manage/clicks", url.Values{"action": {"password"}, "password": {"long-password"}}); code != 400 {
		t.Fatal(code)
	}
}

func TestHTMLImportAndAPILimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
		want bool
	}{
		{"report.html", 25 << 20, true}, {"report.htm", (25 << 20) + 1, false}, {"notes.md", (2 << 20) + 1, false}, {"image.svg", 10, false},
	} {
		if got := renderableImport(tc.name, tc.size); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	in := publication{Title: "Large HTML", Agent: "Codex", OriginalRequest: "Keep the HTML", HTML: "<p>" + strings.Repeat("x", 3<<20) + "</p>"}
	if len(checkFields(in)) == 0 {
		t.Fatal("API accepted HTML over 2 MiB")
	}
	in.ImportedHTML = true
	if problems := checkFields(in); len(problems) != 0 {
		t.Fatal(problems)
	}
}
