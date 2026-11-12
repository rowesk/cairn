package cairn

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"html/template"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func renderFixture(t *testing.T, layout string) (string, renderResult) {
	t.Helper()
	in := publication{Title: "Bulbs for the bedroom", Markdown: reportFixture, Template: layout}
	result, err := renderPublication(&in)
	if err != nil {
		t.Fatal(err)
	}
	if in.Markdown != "" || !strings.HasPrefix(in.HTML, "<!doctype html>") {
		t.Fatal("markdown was not replaced by the page")
	}
	return in.HTML, result
}

func findMain(t *testing.T, page string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	main := firstElement(doc, "main")
	if main == nil {
		t.Fatal("no main element")
	}
	return main
}

func signature(n *html.Node) string {
	s := n.Data
	if class := attr(n, "class"); class != "" {
		s += "." + strings.ReplaceAll(class, " ", ".")
	}
	if kind := attr(n, "data-kind"); kind != "" {
		s += "[" + kind + "]"
	}
	return s
}

// The page's outline, one line per block, is the structural golden for the
// fixture. It changes only when the rendering does.
const fixtureOutline = `h1.title-echo
p
h2
p
div.table-frame
h3
div.table-frame
h4
p
h5
p
h6
p
h2
ol
ul
blockquote
div.callout[verdict]
div.callout[warning]
h2
pre
h2
pre
pre
h2
figure
p
div.figure-grid
h2
div.code-block
div.code-block
div.code-block
h2
p
details
a.report-image
figure
p
a
hr
p
div.footnotes`

func TestReportFixtureRendersEveryElementInEachTemplate(t *testing.T) {
	for _, layout := range []string{"report", "comparison", "visual"} {
		t.Run(layout, func(t *testing.T) {
			page, result := renderFixture(t, layout)
			main := findMain(t, page)
			if attr(main, "data-template") != layout {
				t.Fatalf("template %q", attr(main, "data-template"))
			}
			var outline []string
			for _, c := range elementChildren(main) {
				outline = append(outline, signature(c))
			}
			if got := strings.Join(outline, "\n"); got != fixtureOutline {
				t.Fatalf("outline changed:\n%s", got)
			}
			for _, want := range []string{
				// Footnotes link both ways with ids EPUB readers accept.
				`<sup id="fnref-1"><a class="footnote-ref" href="#fn-1" role="doc-noteref" aria-label="Note 1">1</a></sup>`,
				`<li id="fn-2">`, `href="#fnref-2" role="doc-backlink" aria-label="Back to reference 2"`,
				`<div class="footnotes" role="doc-endnotes" aria-label="Notes">`,
				// Tables are named after the heading above them.
				`role="region" aria-label="What the bulbs cost, table" data-scroll=""`,
				`aria-label="Every bulb, side by side, table"`,
				`<th align="right" scope="col">Price</th>`, "<td class=\"num\">25,000\u00a0h</td>", `<td class="short">E27</td>`,
				`Hot to the touch<br/>after a minute`,
				// Lists, tasks and callouts.
				"<ol>\n<li>Check the beam angle</li>", `<input checked="" type="checkbox" disabled=""/> Fitting measured`,
				`<strong class="callout-label">Verdict<span class="sr-only">:</span></strong> Buy the warm LED.`,
				// Code keeps its language as a label; prose-like languages wrap.
				`<div class="code-block" data-lang="Shell"><pre aria-label="Shell code" tabindex="0" role="group" data-scroll=""><code class="language-sh">`,
				`<div class="code-block"><pre aria-label="Code" tabindex="0" role="group" data-scroll=""><code>Bulb`,
				`<div class="code-block" data-lang="Text" data-wrap="">`,
				// Diagrams and charts wait for the runtime unchanged.
				`<pre><code class="language-mermaid">flowchart LR`, `<pre><code class="language-echarts">{&#34;title&#34;`,
				// Figures, captions and full-size links.
				`<figure><a href="?asset=bulb.png" class="report-image" aria-label="Open image at full size"><img src="?asset=bulb.png" alt="A warm LED bulb glowing on a bedside table"/></a><figcaption>The warm LED at full brightness</figcaption></figure>`,
				`<figcaption>The bulb from above, with its fitting.</figcaption>`,
				// Raw HTML that survives.
				`H<sub>2</sub>O`, `r<sup>2</sup>`, `<kbd>Cmd</kbd>+<kbd>S</kbd>`, `<mark>warm LED</mark>`, `wins.<br/>This sentence`,
				"<details>\n<summary>How the running cost was worked out</summary>", `<img src="?asset=bulb.png" width="120" alt="A small bulb"/>`,
				`<p>A paragraph with a style and a click handler.</p>`, `<a>A link that runs code</a>`,
				// Typography in prose, never in code.
				`maker’s app`, `“Every LED I fit now is warm white, because nobody likes the blue ones after a week.”`,
				`<code>--compare</code>`, `jq &#39;.bulbs[]`,
			} {
				if !strings.Contains(page, want) {
					t.Errorf("missing %s", want)
				}
			}
			for _, gone := range []string{"<script", "<iframe", "<form", "<button", `type="text"`, "onclick", "style=", "javascript:", "–compare", "&lt;details", "&lt;br"} {
				if strings.Contains(strings.SplitN(page, "</style>", 2)[1], gone) {
					t.Errorf("%s survived", gone)
				}
			}
			if want := (renderResult{DroppedTags: []string{"button", "form", "iframe", "input", "script"}, DroppedAttributes: []string{"href", "onclick", "style"}}); !reflect.DeepEqual(result, want) {
				t.Errorf("dropped %+v", result)
			}
			rowHeaders := strings.Count(page, `scope="row"`)
			if layout == "comparison" && rowHeaders != 11 {
				t.Errorf("comparison row headers: %d", rowHeaders)
			}
			if layout != "comparison" && rowHeaders != 0 {
				t.Errorf("%s has row headers", layout)
			}
			// No script in the stored page, so the runtime can load deferred.
			if hasOwnScripts(page) {
				t.Error("stored page carries its own script")
			}
		})
	}
}

func TestReportStylesheetCarriesTemplatesTokensAndPrint(t *testing.T) {
	page, _ := renderFixture(t, "report")
	style := page[strings.Index(page, "<style>")+7 : strings.Index(page, "</style>")]
	if style != reportStyle || strings.Contains(style, "/*") || strings.Contains(style, "\n") {
		t.Fatal("stylesheet is not the minified report.css")
	}
	for _, want := range []string{
		`:root[data-report-scheme="dark"]{color-scheme:dark;--report-paper:#111b22`,
		`--report-ink:`, `--report-muted:`, `--report-rule:`, `--report-accent:`, `--report-code:`, `--report-series:#2468a8,`,
		`main[data-template="comparison"]{--wide:120rem}`, `main[data-template="visual"]{--wide:84rem}`,
		// Headings sit close above the block they introduce; short and numeric columns take only their room.
		`main[data-template] :is(h2,h3,h4,h5,h6) + :is(.table-frame,figure,.figure-grid,.code-block,pre,.callout,.diagram,.chart,blockquote,details){margin-top:1rem}`,
		`:is(th,td):is(.num,.short,[align="right"]){width:1%}`, `:is(th,td):first-child:not(.num,.short){min-width:min(9em,38cqi)}`,
		`.figure-grid>figure{display:grid;grid-row:span 2;grid-template-rows:subgrid`,
		`:root[data-report-palette="mono"]:not([data-report-scheme="dark"]){--report-ink:#111111`,
		`:root:not([data-standalone]) .title-echo{position:absolute`,
		`@media print{`, `@media (max-width:37.5rem){`, `@media (prefers-reduced-motion:reduce){`,
		`text-wrap:balance`, `text-wrap:pretty`, `hyphens:manual`, `font-variant-numeric:lining-nums tabular-nums`,
		`animation-timeline:--cairn-table`, `content:" (" attr(href) ")"`,
		`calc(var(--measure) + 2 * var(--gutter))`,
	} {
		if !strings.Contains(style, want) {
			t.Errorf("stylesheet missing %s", want)
		}
	}
	// Weights: 600 for the sans (headings, labels), 700 only for bold in the Georgia body.
	for _, weight := range regexp.MustCompile(`font-weight:(\d+)`).FindAllStringSubmatch(style, -1) {
		if weight[1] != "400" && weight[1] != "500" && weight[1] != "600" && weight[1] != "700" {
			t.Errorf("stylesheet uses font-weight %s", weight[1])
		}
	}
	if n := strings.Count(style, "font-weight:700"); n != 1 || !strings.Contains(style, "strong,b{font-weight:700}") {
		t.Errorf("only bold body text may be 700, found %d", n)
	}
	// Reports get no fonts over the network and never size from the frame's height.
	for _, banned := range []string{"@font-face", "url(", "@import", "vh", "dvh", "svh"} {
		if strings.Contains(strings.ReplaceAll(style, "attr(href)", ""), banned) {
			t.Errorf("stylesheet uses %s", banned)
		}
	}
}

func TestSanitizerRemovesHostileHTML(t *testing.T) {
	cases := []struct {
		markdown, keep string
		dropped        []string
	}{
		{`<script>alert(1)</script>`, ``, []string{"script"}},
		{`<img src=x onerror=alert(1)>`, `<img src="x"/>`, nil},
		{`<a href="javascript:alert(1)">a</a>`, `<a>a</a>`, nil},
		{`<a href="JaVaScRiPt:alert(1)">a</a>`, `<a>a</a>`, nil},
		{`<a href="jav&#x09;ascript:alert(1)">a</a>`, `<a>a</a>`, nil},
		{`<a href=" &#106;avascript:alert(1)">a</a>`, `<a>a</a>`, nil},
		{`<a href="data:text/html,<script>alert(1)</script>">a</a>`, `<a>a</a>`, nil},
		{`<a href="vbscript:msgbox(1)">a</a>`, `<a>a</a>`, nil},
		{`<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=" alt="s">`, `<img alt="s"/>`, nil},
		{`<img src="//evil.example/x.png" alt="p">`, `<img alt="p"/>`, nil},
		{`<img src="https://example.com/x.png" srcset="javascript:alert(1) 2x" alt="r">`, `<img src="https://example.com/x.png" alt="r"/>`, nil},
		{`<img src="data:image/png;base64,iVBORw0KGgo=" alt="i">`, `<img src="data:image/png;base64,iVBORw0KGgo=" alt="i"/>`, nil},
		{`<svg onload=alert(1)><circle r="2"/></svg>`, ``, []string{"svg"}},
		{`<math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`, ``, []string{"math"}},
		{`<iframe srcdoc="<script>alert(1)</script>"></iframe>`, ``, []string{"iframe"}},
		{`<object data="x.swf"></object><embed src="x.swf">`, ``, []string{"embed", "object"}},
		{`<style>@import url(https://evil.example/x.css)</style>`, ``, []string{"style"}},
		{`<div style="background:url(javascript:alert(1))">d</div>`, `<div>d</div>`, nil},
		{`<form action="https://evil.example"><input name="x"><button formaction="javascript:alert(1)">b</button></form>`, ``, []string{"button", "form", "input"}},
		{`<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`, ``, []string{"meta"}},
		{`<base href="https://evil.example/"><link rel="stylesheet" href="https://evil.example/x.css">`, ``, []string{"base", "link"}},
		{`<details open ontoggle=alert(1)><summary>s</summary>d</details>`, `<details open=""><summary>s</summary>d</details>`, nil},
		{`<a href="https://example.com/x" target="_blank" rel="opener">ok</a>`, `<a href="https://example.com/x">ok</a>`, nil},
		{`<noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`, ``, []string{"noscript"}},
		{`<template><img src=x onerror=alert(1)></template>`, ``, []string{"template"}},
		{`<p>a<!-- <script>alert(1)</script> -->b</p>`, `<p>ab</p>`, nil},
		{`<custom-card onclick="x()">Kept text</custom-card>`, `Kept text`, []string{"custom-card"}},
		{`<center><font color="red">old</font></center>`, `old`, []string{"center", "font"}},
		{`<input type="checkbox" checked onclick="x()">`, `<input type="checkbox" checked=""`, nil},
		{`<textarea><script>alert(1)</script></textarea><select><option>o</option></select>`, ``, []string{"select", "textarea"}},
		{`<a href="mailto:a@example.com">m</a> <a href="tel:+441234">t</a> <a href="#fn-1">f</a> <a href="?asset=a.png">a</a>`, `<a href="mailto:a@example.com">m</a> <a href="tel:+441234">t</a> <a href="#fn-1">f</a> <a href="?asset=a.png">a</a>`, nil},
		{`<td><table><tr><td style="color:red" onmouseover="x()" colspan="2" rowspan="x">c</td></tr></table>`, `<td colspan="2" class="short">c</td>`, nil},
		{`<ol start="3" type="i" reversed><li value="5">v</li></ol>`, `<ol start="3" type="i" reversed=""><li value="5">v</li></ol>`, nil},
		{`<p dir="sideways" lang="en" title="t" role="button" aria-label="l" data-x="1">p</p>`, `<p lang="en" title="t" aria-label="l">p</p>`, nil},
		{`<img src="?asset=a.png" width="200" height="100%" alt="w"> <img src="?asset=a.png" width="calc(1px)" alt="c">`, `<img src="?asset=a.png" width="200" height="100%" alt="w"/>`, nil},
	}
	dangerous := []string{"<script", "onerror", "onclick", "onload", "ontoggle", "onmouseover", "javascript:", "vbscript", "<iframe", "<svg", "<math", "<style", "<object", "<embed", "<form", "<meta", "<base", "<link", "srcdoc", "formaction", "data:text", "srcset", "style=", "target=", "<textarea", "<noscript", "<template", "evil.example"}
	for _, c := range cases {
		body, log, err := renderMarkdown(c.markdown, "", "report")
		if err != nil {
			t.Fatalf("%s: %v", c.markdown, err)
		}
		lower := strings.ToLower(body)
		for _, bad := range dangerous {
			if strings.Contains(lower, bad) {
				t.Errorf("%s\nkept %s in %s", c.markdown, bad, body)
			}
		}
		if c.keep != "" && !strings.Contains(body, c.keep) {
			t.Errorf("%s\nwant %s in %s", c.markdown, c.keep, body)
		}
		if got := sortedKeys(log.tags); !reflect.DeepEqual(got, c.dropped) && !(len(got) == 0 && len(c.dropped) == 0) {
			t.Errorf("%s\ndropped tags %v, want %v", c.markdown, got, c.dropped)
		}
		// Whatever the sanitiser keeps is already clean: a second pass removes nothing.
		if again := resanitize(t, c.markdown); len(again.tags)+len(again.attributes) > 0 {
			t.Errorf("%s\nsecond pass dropped %v %v", c.markdown, again.tags, again.attributes)
		}
	}
}

// resanitize renders Markdown, sanitizes it, and sanitizes the result again,
// before any decoration adds Cairn's own attributes.
func resanitize(t *testing.T, markdown string) dropLog {
	t.Helper()
	var first, again dropLog
	var rendered bytes.Buffer
	if err := markdownEngine.Convert([]byte(markdown), &rendered); err != nil {
		t.Fatal(err)
	}
	root, err := parseBody(rendered.String())
	if err != nil {
		t.Fatal(err)
	}
	sanitizeChildren(root, &first)
	var clean bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&clean, c)
	}
	root, err = parseBody(clean.String())
	if err != nil {
		t.Fatal(err)
	}
	sanitizeChildren(root, &again)
	return again
}

func TestSanitizerReportsDroppedAttributes(t *testing.T) {
	_, log, err := renderMarkdown(`<p style="x" onclick="y()" data-z="1">a</p> <a href="javascript:x" target="_blank">b</a>`, "", "report")
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(log.attributes); !reflect.DeepEqual(got, []string{"data-z", "href", "onclick", "style"}) {
		t.Fatalf("dropped attributes %v", got)
	}
}

func TestInvalidChartJSONIsRejectedWithItsLine(t *testing.T) {
	fence := "```"
	cases := []struct {
		markdown string
		block    int
		line     int
		reason   string
	}{
		{"# Costs\n\n" + fence + "echarts\n{\"series\": [{\"type\": \"bar\", \"data\": [1, 2,]}]}\n" + fence, 1, 4, "invalid character ']'"},
		{"Intro\n\n" + fence + "echarts\n{\"series\": []}\n" + fence + "\n\n" + fence + "echarts\n{\n  title: {text: 'Cost'}\n}\n" + fence, 2, 9, "invalid character 't'"},
		{fence + "echarts\n{\"tooltip\": {\"formatter\": function (p) { return p.name }}}\n" + fence, 1, 2, "invalid character 'u'"},
		{fence + "echarts\n// a comment\n{}\n" + fence, 1, 2, "invalid character '/'"},
		{fence + "echarts\n[1, 2, 3]\n" + fence, 1, 2, "one JSON object"},
		{fence + "echarts\n\n" + fence, 1, 2, "empty"},
		{"- item\n\n  " + fence + "ECharts\n  {\"series\": [}\n  " + fence, 1, 4, "invalid character '}'"},
	}
	for _, c := range cases {
		in := publication{Title: "Costs", Markdown: c.markdown}
		_, err := renderPublication(&in)
		var chart *chartError
		if !errors.Is(err, errInvalidChart) || !errors.As(err, &chart) {
			t.Fatalf("%q: %v", c.markdown, err)
		}
		if chart.Block != c.block || chart.Line != c.line || !strings.Contains(chart.Reason, c.reason) {
			t.Errorf("%q: %+v", c.markdown, chart)
		}
		if !strings.HasPrefix(err.Error(), "echarts block ") || !strings.Contains(err.Error(), " is not valid chart JSON: ") {
			t.Errorf("message %q", err.Error())
		}
		if in.HTML != "" {
			t.Error("a rejected page was rendered")
		}
	}
	valid := publication{Title: "Costs", Markdown: fence + "echarts\n{\"series\": [{\"type\": \"line\", \"data\": [1, 2]}]}\n" + fence + "\n\n" + fence + "json\n{not checked}\n" + fence}
	if _, err := renderPublication(&valid); err != nil {
		t.Fatal(err)
	}
}

func TestTitleRuleHidesOnlyAnOpeningRepeat(t *testing.T) {
	cases := []struct {
		title, markdown string
		hidden          bool
	}{
		{"Bulbs for the bedroom", "# Bulbs for the bedroom\n\nText", true},
		{"Bulbs for the Bedroom.", "#   bulbs  for the bedroom\n\nText", true},
		{"Where the heat goes: insulating a 1930s semi", "# Where the heat goes\n\nText", true},
		{"Monitors – which to buy", "# Monitors\n\nText", true},
		{"Bob's report", "# Bob's report\n\nText", true},
		{"The plan for the garden office in spring", "# The plan for\n\nText", true},
		{"The plan", "# The\n\nText", false},
		{"Bulb comparison", "# Bulbs\n\nText", false},
		{"Bulbs", "# Bulbs for the bedroom\n\nText", false},
		{"A weekend away", "# Leave the morning open\n\nText", false},
		{"Bulbs for the bedroom", "Intro first.\n\n# Bulbs for the bedroom", false},
		{"Bulbs for the bedroom", "## Bulbs for the bedroom\n\nText", false},
		{"", "# Untitled\n\nText", false},
	}
	for _, c := range cases {
		in := publication{Title: c.title, Markdown: c.markdown}
		if _, err := renderPublication(&in); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(in.HTML, `class="title-echo"`); got != c.hidden {
			t.Errorf("%q / %q: hidden %v", c.title, c.markdown, got)
		}
	}
	// Bodies built without Markdown, such as imported documents, follow the same rule.
	var out bytes.Buffer
	if err := reportTemplate.Execute(&out, struct {
		Title, Layout string
		Body          template.HTML
	}{"Lease", "report", `<h1>Lease</h1><p>Saved.</p>`}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `<main data-template="report"><h1 class="title-echo">Lease</h1><p>Saved.</p></main>`) {
		t.Fatal(out.String())
	}
}

func TestTitleEchoStaysOutOfContents(t *testing.T) {
	in := publication{Title: "Garden office", Markdown: "# Garden office\n\n# Planning\n\n## Size\n\n## Power\n\n# Building\n\n## Base"}
	if _, err := renderPublication(&in); err != nil {
		t.Fatal(err)
	}
	_, headings := reportHeadings(in.HTML)
	if len(headings) != 6 || !headings[0].Title || headings[1].Title {
		t.Fatalf("headings %+v", headings)
	}
	var walk func([]*contentsEntry) string
	walk = func(entries []*contentsEntry) string {
		var parts []string
		for _, e := range entries {
			parts = append(parts, e.ID+"("+walk(e.Children)+")")
		}
		return strings.Join(parts, " ")
	}
	if got := walk(reportContents(headings, "g")); got != "planning(size() power()) building(base())" {
		t.Fatalf("contents %s", got)
	}
}

func TestFixtureBuildsAnEPUBWithFootnotes(t *testing.T) {
	page, _ := renderFixture(t, "report")
	book, err := buildBook(savedPage{ID: "id", Slug: "bulbs", Title: "Bulbs for the bedroom", Agent: "Claude", CreatedAt: "2026-11-12T10:00:00Z"}, page, map[string][]byte{"bulb.png": fixtureImage()})
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		files[f.Name] = string(b)
	}
	xhtml := files["OEBPS/page.xhtml"]
	decoder := xml.NewDecoder(strings.NewReader(xhtml))
	for {
		if _, err := decoder.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("page.xhtml is not XML: %v", err)
		}
	}
	for _, want := range []string{
		`id="bulbs-for-the-bedroom">Bulbs for the bedroom</h1><p class="byline">Prepared by Claude · 12 Nov 2026</p>`,
		`<sup id="fnref-1"><a aria-label="Note 1" class="footnote-ref" href="#fn-1" role="doc-noteref">1</a></sup>`,
		`<li id="fn-1">`, `href="#fnref-1" role="doc-backlink"`,
		`<figcaption>The warm LED at full brightness</figcaption>`, `<img src="images/image-1.png" alt="A warm LED bulb glowing on a bedside table"/>`,
		`<sub>2</sub>`, `<kbd>Cmd</kbd>`, `<strong class="callout-label">Verdict`,
	} {
		if !strings.Contains(xhtml, want) {
			t.Errorf("EPUB missing %s", want)
		}
	}
	nav := files["OEBPS/nav.xhtml"]
	if strings.Count(nav, "Bulbs for the bedroom") != 2 || !strings.Contains(nav, `href="page.xhtml#what-the-bulbs-cost"`) {
		t.Errorf("nav %s", nav)
	}
}
