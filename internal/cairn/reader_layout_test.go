package cairn

import (
	"html/template"
	"strings"
	"testing"
)

func TestReportShowsTitle(t *testing.T) {
	long := "Comparison of every 27-inch 4K monitor under £400"
	for _, c := range []struct {
		name     string
		first    outlineHeading
		title    string
		template bool
		want     bool
	}{
		{"template hides an echo it marked", outlineHeading{Level: 1, Text: "Where the heat goes", Title: true}, "Where the heat goes: a 1930s semi", true, false},
		{"older template page shows its echo", outlineHeading{Level: 1, Text: "Where the heat goes"}, "Where the heat goes: a 1930s semi", true, true},
		{"template h1 with other words is a heading", outlineHeading{Level: 1, Text: "Monitors"}, long, true, false},
		{"custom HTML opening h1", outlineHeading{Level: 1, Text: "Three routers, one obvious pick"}, "Router shortlist", false, true},
		{"custom HTML opening h2", outlineHeading{Level: 2, Text: "Why"}, "Router shortlist", false, false},
	} {
		if got := reportShowsTitle([]outlineHeading{c.first}, c.title, c.template); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if reportShowsTitle(nil, "Anything", false) {
		t.Error("a report without headings has no title")
	}
}

func TestReaderTitleKeepsCompoundsWhole(t *testing.T) {
	for in, want := range map[string]template.HTML{
		"A 27-inch USB-C screen":                    `A <span class="compound">27-inch</span> <span class="compound">USB-C</span> screen`,
		"Before - after":                            `Before - after`,
		"Tom & Jerry <script>":                      `Tom &amp; Jerry &lt;script&gt;`,
		"state-of-the-art-and-then-some-more-words": `state-of-the-art-and-then-some-more-words`,
	} {
		if got := readerTitle(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

func TestReportLayoutNamesTheTemplate(t *testing.T) {
	for doc, want := range map[string]struct {
		layout   string
		template bool
	}{
		`<main data-template="comparison"><p>x</p></main>`: {"comparison", true},
		`<main data-template="report">`:                    {"report", true},
		`<h1>Custom</h1><main>x</main>`:                    {"", false},
	} {
		if layout, template := reportLayout(doc); layout != want.layout || template != want.template {
			t.Errorf("%s: %q %v, want %q %v", doc, layout, template, want.layout, want.template)
		}
	}
}

func TestReportHeightLimitFollowsWhatThePageHolds(t *testing.T) {
	note := reportHeightLimit(`<p>One line.</p>`)
	// Styles and scripts are not reading text, however long.
	styled := reportHeightLimit(`<style>` + strings.Repeat("p{color:red}", 5000) + `</style><script>` + strings.Repeat("x();", 5000) + `</script><p>One line.</p>`)
	long := reportHeightLimit(strings.Repeat(`<p>A paragraph of reading text that runs on for a while.</p>`, 400))
	figures := reportHeightLimit(strings.Repeat(`<figure><img src="a.png"></figure>`, 20))
	diagrams := reportHeightLimit(strings.Repeat(`<pre><code class="language-mermaid">flowchart LR</code></pre>`, 10))
	if note > 9000 || styled != note {
		t.Errorf("a one-line note may claim %d (styled %d)", note, styled)
	}
	if long < 60000 || figures < 40000 || diagrams < 20000 {
		t.Errorf("limits too tight: long %d, figures %d, diagrams %d", long, figures, diagrams)
	}
	if huge := reportHeightLimit(strings.Repeat(`<p>words words words words words</p>`, 50000)); huge != 200000 {
		t.Errorf("limit not capped: %d", huge)
	}
}

func TestTableHeadingsKeepHyphenatedWordsWhole(t *testing.T) {
	out, _, err := renderMarkdown("| Model | Price (3-pack) | Wi-Fi standard |\n|---|---:|---|\n| Deco | £449 | Wi-Fi 7 |", "Routers", "comparison")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`Price <span class="nobr">(3-pack)</span>`, `<span class="nobr">Wi-Fi</span> standard`, `<td class="short">Wi-Fi 7</td>`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}
