package cairn_test

import (
	"strings"
	"testing"
)

func TestReaderListsNestedContentsThatMatchDeliveredHeadings(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Pet insurance")
	delete(in, "html")
	in["markdown"] = "# Pet insurance\n\n## Should you insure\n\n### Self-insuring\n\n## What it costs\n\n##### Excess\n\n## Next steps"
	in["slug"] = "pet"
	if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
		t.Fatalf("publish: %d %s", status, body)
	}
	_, _, reader := request(t, srv, "GET", "/pet", nil, "")
	want := `<ol id="toc-list"><li><a href="/_content/pet#should-you-insure" target="report" data-id="should-you-insure">Should you insure</a><ol><li><a href="/_content/pet#self-insuring" target="report" data-id="self-insuring">Self-insuring</a></li></ol></li><li><a href="/_content/pet#what-it-costs" target="report" data-id="what-it-costs">What it costs</a><ol><li><a href="/_content/pet#excess" target="report" data-id="excess">Excess</a></li></ol></li><li><a href="/_content/pet#next-steps" target="report" data-id="next-steps">Next steps</a></li></ol>`
	if !strings.Contains(string(reader), want) || !strings.Contains(string(reader), `class="reading-shell has-contents"`) {
		t.Fatalf("reader contents:\n%s", reader)
	}
	_, _, content := request(t, srv, "GET", "/_content/pet", nil, "")
	for _, id := range []string{"should-you-insure", "self-insuring", "what-it-costs", "excess", "next-steps"} {
		if !strings.Contains(string(content), `id="`+id+`"`) {
			t.Fatalf("content lacks %s", id)
		}
	}
}
