package cairn

import (
	"strings"
	"testing"
)

func TestReportLinksPreservesReportAndSeparatesWebDestinations(t *testing.T) {
	in := `<!doctype html><style>a{color:red}</style><script>const x='<a href="https://ignore.test">';</script><a href="https://example.org/?x=1&amp;y=2" target="_self" rel="opener nofollow">External</a><a href="//example.org">Protocol relative</a><a href="#section">Section</a><a href="?asset=photo.png">Photo</a><a href="mailto:test@example.org">Mail</a>`
	out := reportLinks(in)
	if strings.Count(out, `target="_blank"`) != 2 || !strings.Contains(out, `rel="noopener noreferrer nofollow"`) {
		t.Fatal(out)
	}
	for _, unchanged := range []string{`<style>a{color:red}</style>`, `<script>const x='<a href="https://ignore.test">';</script>`, `<a href="#section">Section</a>`, `<a href="?asset=photo.png">Photo</a>`, `<a href="mailto:test@example.org">Mail</a>`} {
		if !strings.Contains(out, unchanged) {
			t.Fatalf("changed %s", unchanged)
		}
	}
	if reportLinks(out) != out {
		t.Fatal("not idempotent")
	}
}
