package cairn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pages saved before slugs became unique regardless of case keep working:
// lookups stay exact, and only new slugs must differ by more than case.
func TestCaseVariantSlugsSavedEarlierStillOpen(t *testing.T) {
	s, err := Open(Config{DataDir: t.TempDir(), PublisherToken: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, slug := range []string{"lamp-report", "Lamp-Report"} {
		object, err := s.writeObject(publication{HTML: "<p>" + slug + "</p>"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO pages (id,slug,title,agent,original_request,created_at,object) VALUES (?,?,?,?,?,?,?)`, string(rune('a'+i)), slug, slug, "Agent", "r", "2026-01-01T00:00:00Z", object); err != nil {
			t.Fatal(err)
		}
	}
	for _, slug := range []string{"lamp-report", "Lamp-Report"} {
		p, err := s.getPage(slug)
		if err != nil || p.Title != slug {
			t.Errorf("%s: %+v %v", slug, p, err)
		}
		page, _, err := s.pageResource(slug)
		if err != nil || page.Slug != slug || page.UpdatedAt != "2026-01-01T00:00:00Z" {
			t.Errorf("%s resource: %+v %v", slug, page, err)
		}
	}
	for slug, want := range map[string]string{"LAMP-REPORT": "lamp-report", "Lamp-Report": "Lamp-Report", "lamp-report": "lamp-report", "other": ""} {
		if got, err := s.slugHolder(slug); err != nil || got != want {
			t.Errorf("slugHolder(%q) = %q, want %q", slug, got, want)
		}
	}
	if problem, _ := s.linkNameProblem("LAMP-report", ""); problem == 0 {
		t.Error("a link name differing only in case was accepted")
	}
}

func TestUnmarkTurnsHighlightsIntoSpans(t *testing.T) {
	text, spans := unmark(string(marked("Café &lt; " + markOpen + "quartz" + markClose + "ite & " + markOpen + "oak" + markClose)))
	if text != "Café &lt; quartzite & oak" || len(spans) != 2 || spans[0] != [2]int{10, 16} || spans[1] != [2]int{22, 25} {
		t.Fatalf("%q %v", text, spans)
	}
}

// The reference and the setup text name every field, code and warning the
// server has, so neither drifts from the code.
func TestDocsNameEveryFieldCodeAndWarning(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "publishing.md"))
	if err != nil {
		t.Fatal(err)
	}
	docs := string(raw)
	var names []string
	for _, f := range publicationFields {
		names = append(names, f.Name)
	}
	names = append(names, apiErrorCodes...)
	names = append(names, fieldErrorCodes...)
	names = append(names, warningCodes...)
	for _, name := range names {
		if !strings.Contains(docs, "`"+name+"`") {
			t.Errorf("docs/publishing.md does not name %s", name)
		}
	}
	for _, limit := range []string{"16 MiB", "2 MiB", "4 MiB", "300 bytes", "32 KiB", "40 words", "80 bytes", "100 bytes", "24 hours"} {
		if !strings.Contains(docs, limit) {
			t.Errorf("docs/publishing.md does not state %s", limit)
		}
	}
	for _, field := range publicationFields {
		if field.Name == "agent" || field.Name == "capabilities" || field.Name == "slug" || field.Name == "html" || field.Name == "request_source" {
			continue
		}
		if !strings.Contains(agentSetupGuide, "`"+field.Name+"`") && !strings.Contains(agentSetupGuide, `"`+field.Name+`"`) {
			t.Errorf("setup text does not name %s", field.Name)
		}
	}
}
