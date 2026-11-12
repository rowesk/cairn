package cairn

import (
	"strings"
	"testing"
)

func TestReportHeadingsAddsStableIDsAndKeepsOtherBytes(t *testing.T) {
	in := `<!doctype html><style>h2{color:red}</style><h1 class="t">Pet insurance</h1><nav><h2>On this page</h2></nav><h2 id="costs">What it <em>costs</em></h2><h3>Lifetime &amp; time-limited</h3><h2>Section 7</h2><h2>Section 7</h2><p id="section-7-1">taken</p><h4 hidden>Hidden</h4><h5> </h5><script>document.write("<h2>not a heading</h2>")</script>`
	out, headings := reportHeadings(in)
	for _, want := range []string{`<h1 class="t" id="pet-insurance">Pet insurance</h1>`, `<h2 id="costs">What it <em>costs</em></h2>`, `<h3 id="lifetime--time-limited">`, `<h2 id="section-7">Section 7</h2>`, `<h2 id="section-7-2">Section 7</h2>`, `<nav><h2 id="on-this-page">`, `<style>h2{color:red}</style>`, `<script>document.write("<h2>not a heading</h2>")</script>`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	got := []string{}
	for _, h := range headings {
		got = append(got, string(rune('0'+h.Level))+" "+h.ID+" "+h.Text)
	}
	want := []string{"1 pet-insurance Pet insurance", "2 costs What it costs", "3 lifetime--time-limited Lifetime & time-limited", "2 section-7 Section 7", "2 section-7-2 Section 7"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("headings %q", got)
	}
	if again, _ := reportHeadings(out); again != out {
		t.Fatal("not idempotent")
	}
}

func TestReportContentsNestsUnderNearestShallowerHeading(t *testing.T) {
	headings := []outlineHeading{{Level: 1, ID: "title", Text: "Title"}, {Level: 2, ID: "a", Text: "A"}, {Level: 3, ID: "a1", Text: "A1"}, {Level: 5, ID: "deep", Text: "Deep"}, {Level: 3, ID: "a2", Text: "A2"}, {Level: 2, ID: "b", Text: "B"}, {Level: 4, ID: "b1", Text: "B1"}}
	contents := reportContents(headings, "pet")
	var walk func([]*contentsEntry, string) string
	walk = func(entries []*contentsEntry, indent string) string {
		var b strings.Builder
		for _, e := range entries {
			b.WriteString(indent + e.ID + "\n")
			b.WriteString(walk(e.Children, indent+"  "))
		}
		return b.String()
	}
	if got := walk(contents, ""); got != "a\n  a1\n    deep\n  a2\nb\n  b1\n" {
		t.Fatalf("tree:\n%s", got)
	}
	if contents[0].Href != "/_content/pet#a" {
		t.Fatal(contents[0].Href)
	}
	if reportContents([]outlineHeading{{Level: 1, ID: "x", Text: "X"}, {Level: 2, ID: "a", Text: "A"}, {Level: 2, ID: "b", Text: "B"}}, "pet") != nil {
		t.Fatal("short outline should have no contents")
	}
	if got := reportContents([]outlineHeading{{Level: 1, ID: "x", Text: "X"}, {Level: 1, ID: "y", Text: "Y"}, {Level: 2, ID: "a", Text: "A"}}, "pet"); len(got) != 2 {
		t.Fatal("several h1 headings are sections, not the page title")
	}
}
