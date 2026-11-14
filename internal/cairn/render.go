package cairn

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// renderResult tells the publisher what Cairn changed while turning Markdown
// into a page. The page is saved either way.
type renderResult struct {
	// DroppedTags names raw HTML elements removed from the Markdown, sorted.
	DroppedTags []string
	// DroppedAttributes names attributes removed from allowed elements, such
	// as onclick, style, or an href with a javascript: address.
	DroppedAttributes []string
}

// errInvalidChart matches every chartError through errors.Is.
var errInvalidChart = errors.New("invalid chart")

// chartError rejects an echarts block whose options are not a JSON object.
type chartError struct {
	Block  int    // position among the Markdown's echarts blocks, from 1
	Line   int    // Markdown line where the problem is
	Reason string // what is wrong, in plain words
}

func (e *chartError) Error() string {
	return fmt.Sprintf("echarts block %d on line %d is not valid chart JSON: %s", e.Block, e.Line, e.Reason)
}

func (e *chartError) Unwrap() error { return errInvalidChart }

var markdownEngine = goldmark.New(
	goldmark.WithExtensions(
		extension.Linkify,
		extension.Strikethrough,
		extension.TaskList,
		extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
		extension.NewFootnote(),
		// Curly quotes, apostrophes, --- and ... only: -- and << >> appear in
		// commands and comparisons too often to rewrite. Code is never touched.
		extension.NewTypographer(extension.WithTypographicSubstitutions(map[extension.TypographicPunctuation][]byte{
			extension.EnDash: nil, extension.LeftAngleQuote: nil, extension.RightAngleQuote: nil,
		})),
	),
	// Raw HTML passes through here and is then cleaned by sanitizeChildren.
	goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
)

func preparePublication(in *publication) error {
	_, err := renderPublication(in)
	return err
}

// renderPublication validates the publication and, for Markdown, replaces it
// with the rendered page.
func renderPublication(in *publication) (renderResult, error) {
	var result renderResult
	if (strings.TrimSpace(in.HTML) == "") == (strings.TrimSpace(in.Markdown) == "") {
		return result, fmt.Errorf("supply exactly one of html or markdown")
	}
	if in.Template != "" && in.Template != "report" && in.Template != "comparison" && in.Template != "visual" {
		return result, fmt.Errorf("unknown template")
	}
	if len(in.Markdown) > 2<<20 {
		return result, fmt.Errorf("markdown exceeds 2 MiB")
	}
	if in.Markdown == "" {
		return result, nil
	}
	layout := in.Template
	if layout == "" {
		layout = "report"
	}
	body, log, err := renderMarkdown(in.Markdown, in.Title, layout, in.WebImages)
	if err != nil {
		return result, err
	}
	var output bytes.Buffer
	err = reportTemplate.Execute(&output, struct {
		Title, Layout string
		Body          template.HTML
	}{in.Title, layout, template.HTML(body)})
	if err != nil {
		return result, err
	}
	in.HTML = output.String()
	in.Markdown = ""
	result.DroppedTags = sortedKeys(log.tags)
	result.DroppedAttributes = sortedKeys(log.attributes)
	return result, nil
}

// renderMarkdown turns Markdown into the inside of a report's <main>.
func renderMarkdown(markdown, title, layout string, images ...map[string]string) (string, dropLog, error) {
	var log dropLog
	source := []byte(markdown)
	doc := markdownEngine.Parser().Parse(text.NewReader(source))
	if err := checkCharts(doc, source); err != nil {
		return "", log, err
	}
	var rendered bytes.Buffer
	if err := markdownEngine.Renderer().Render(&rendered, source, doc); err != nil {
		return "", log, err
	}
	root, err := parseBody(rendered.String())
	if err != nil {
		return "", log, err
	}
	sanitizeChildren(root, &log)
	if len(images) > 0 {
		mapWebImages(root, images[0])
	}
	decorateReport(root, title, layout)
	var content bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&content, c); err != nil {
			return "", log, err
		}
	}
	return content.String(), log, nil
}

// parseBody parses an HTML fragment as body content under a detached root,
// so nothing is moved into a <head>.
func parseBody(fragment string) (*html.Node, error) {
	nodes, err := html.ParseFragment(strings.NewReader(fragment), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return nil, err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	return root, nil
}

// checkCharts rejects echarts blocks that the browser could not parse, so the
// publisher hears about it instead of the reader finding an empty chart.
func checkCharts(doc gast.Node, source []byte) error {
	block := 0
	return gast.Walk(doc, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		fence, ok := n.(*gast.FencedCodeBlock)
		if !entering || !ok || !strings.EqualFold(string(fence.Language(source)), "echarts") {
			return gast.WalkContinue, nil
		}
		block++
		var options bytes.Buffer
		lines := fence.Lines()
		first := bytes.Count(source[:fence.Info.Segment.Start], []byte("\n")) + 2
		for i := 0; i < lines.Len(); i++ {
			segment := lines.At(i)
			options.Write(segment.Value(source))
		}
		fail := func(offset int, reason string) error {
			line := first + bytes.Count(options.Bytes()[:min(max(offset, 0), options.Len())], []byte("\n"))
			return &chartError{Block: block, Line: line, Reason: reason}
		}
		if len(bytes.TrimSpace(options.Bytes())) == 0 {
			return gast.WalkStop, fail(0, "the block is empty; put the chart's option object inside it")
		}
		var value any
		if err := json.Unmarshal(options.Bytes(), &value); err != nil {
			var syntax *json.SyntaxError
			if errors.As(err, &syntax) {
				return gast.WalkStop, fail(int(syntax.Offset)-1, syntax.Error()+"; use double-quoted keys and no comments, functions or trailing commas")
			}
			return gast.WalkStop, fail(0, err.Error())
		}
		if _, ok := value.(map[string]any); !ok {
			return gast.WalkStop, fail(0, "the options must be one JSON object, such as {\"series\": [...]}")
		}
		return gast.WalkContinue, nil
	})
}

// decorateReport adds what Markdown cannot express: scroll regions, figures,
// callouts, row headers, code labels and the title rule.
func decorateReport(root *html.Node, title, layout string) {
	markTitleEcho(root, title)
	footnotes(root)
	callouts(root)
	figures(root)
	imageLinks(root, false)
	tables(root, layout)
	codeBlocks(root)
	keepUnitsTogether(root)
}

// unitAfter matches a number, the space after it and what follows: a unit
// of measure or a multiplication sign. unitBefore matches a multiplication
// sign and the number after it. The character after a unit must not carry
// the word on (checked in keepUnitsTogether), so "5 men" keeps its space.
var (
	unitAfter  = regexp.MustCompile(`(\d) ((?:k|M|G)?Wh|(?:k|M|G)?W|kVA|mAh|mA|Ah|V|(?:k|M|G)?Hz|°[CF]|℃|dB(?:A|\(A\))?|(?:m|c|k)?m[²³]?|ft|mi|(?:m|k)?g|lbs?|oz|m?L|ml|cl|litres?|(?:K|k|M|G|T|P)i?B|(?:k|M|G)?bps|(?:M|G)b/s|MB/s|ms|mins?|hrs?|h|mph|km/h|rpm|%|ppm|lm|lx|(?:k|M)?Pa|bar|psi|kcal|(?:k|M)?J|px|×)`)
	unitBefore = regexp.MustCompile(`(×) (\d)`)
	timesX     = regexp.MustCompile(`(\d) x (\d)`)
)

// keepUnitsTogether joins a number to the unit or multiplication sign after
// it with a no-break space, so "6.1 kW", "45 °C" and "2 × 8 TB" never break
// across lines. It runs in prose, headings and table cells, never in code,
// diagrams or charts, and leaves currency, years and ordinary words alone.
func keepUnitsTogether(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.ElementNode:
			switch c.Data {
			case "pre", "code", "kbd", "samp", "script", "style", "svg", "math", "textarea":
				continue
			}
			keepUnitsTogether(c)
		case html.TextNode:
			c.Data = joinUnits(c.Data)
		}
	}
}

func joinUnits(text string) string {
	if !strings.ContainsAny(text, "0123456789") {
		return text
	}
	var out strings.Builder
	at := 0
	for _, m := range unitAfter.FindAllStringSubmatchIndex(text, -1) {
		unitEnd := m[5]
		if next, _ := utf8.DecodeRuneInString(text[unitEnd:]); unitEnd < len(text) && (unicode.IsLetter(next) || unicode.IsDigit(next)) && text[m[4]:m[5]] != "×" {
			continue
		}
		space := m[3]
		out.WriteString(text[at:space])
		out.WriteString("\u00a0")
		at = space + 1
	}
	out.WriteString(text[at:])
	joined := unitBefore.ReplaceAllString(out.String(), "$1\u00a0$2")
	return timesX.ReplaceAllString(joined, "$1\u00a0x\u00a0$2")
}

func element(tag string, attrs ...string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
	for i := 0; i+1 < len(attrs); i += 2 {
		n.Attr = append(n.Attr, html.Attribute{Key: attrs[i], Val: attrs[i+1]})
	}
	return n
}

func wrapNode(parent, child, wrapper *html.Node) {
	parent.InsertBefore(wrapper, child)
	parent.RemoveChild(child)
	wrapper.AppendChild(child)
}

func setAttr(n *html.Node, key, value string) {
	for i, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func addClass(n *html.Node, class string) {
	if !hasClass(n, class) {
		setAttr(n, "class", strings.TrimSpace(attr(n, "class")+" "+class))
	}
}

// textOf returns an element's text with whitespace collapsed.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func elementChildren(n *html.Node) []*html.Node {
	var children []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			children = append(children, c)
		}
	}
	return children
}

// onlyChild returns n's single element child when everything else is
// whitespace.
func onlyChild(n *html.Node) *html.Node {
	var only *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) == "":
		case c.Type == html.ElementNode && only == nil:
			only = c
		default:
			return nil
		}
	}
	return only
}

func each(n *html.Node, tag string, visit func(*html.Node)) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && c.Data == tag {
			visit(c)
		} else {
			each(c, tag, visit)
		}
		c = next
	}
}

// The title rule: when a report opens with an h1 that repeats the page title,
// or is the start of it ("Monitors" for "Monitors: which to buy"), the h1 gets
// class title-echo. The template hides it visually inside Cairn's reader,
// where the cover already shows the title. It stays in the HTML for screen
// readers, the EPUB, previews of the source and the standalone page.

func markTitleEcho(root *html.Node, title string) bool {
	first := firstContent(root)
	if first == nil || first.Data != "h1" || !echoesTitle(textOf(first), title) {
		return false
	}
	addClass(first, "title-echo")
	return true
}

func firstContent(root *html.Node) *html.Node {
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) != "" {
			return nil
		}
	}
	return nil
}

var titleSeparators = ":-–—,(|.;/"

func titleKey(s string) string {
	s = strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`, "–", "-", "—", "-", " ", " ").Replace(strings.ToLower(s))
	return strings.TrimRight(strings.Join(strings.Fields(s), " "), " .:;,!?-")
}

func echoesTitle(heading, title string) bool {
	h, t := titleKey(heading), titleKey(title)
	if h == "" || t == "" {
		return false
	}
	if h == t {
		return true
	}
	if !strings.HasPrefix(t, h) {
		return false
	}
	rest := t[len(h):]
	next, _ := utf8.DecodeRuneInString(rest)
	if unicode.IsLetter(next) || unicode.IsDigit(next) {
		return false
	}
	trimmed := strings.TrimLeft(rest, " ")
	if trimmed != "" && strings.ContainsRune(titleSeparators, []rune(trimmed)[0]) {
		return true
	}
	return len(strings.Fields(h)) >= 3
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{"reportBody": reportBody}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title><style>` + reportStyle + `</style></head><body><main data-template="{{.Layout}}">{{reportBody .Title .Body}}</main></body></html>`))

// reportBody applies the title rule to bodies that did not come through
// renderMarkdown, such as imported documents.
func reportBody(title string, body template.HTML) template.HTML {
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "<h1") || strings.Contains(trimmed[:min(len(trimmed), 200)], "title-echo") {
		return body
	}
	root, err := parseBody(string(body))
	if err != nil || !markTitleEcho(root, title) {
		return body
	}
	var out bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if html.Render(&out, c) != nil {
			return body
		}
	}
	return template.HTML(out.String())
}

//go:embed report.css
var reportCSS string

var reportStyle = minifyCSS(reportCSS)

// minifyCSS drops comments and the whitespace the stylesheet does not need.
// Each stored page carries its own copy.
func minifyCSS(css string) string {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	css = strings.Join(strings.Fields(css), " ")
	for _, sep := range []string{"{", "}", ";", ",", ">"} {
		css = strings.ReplaceAll(css, sep+" ", sep)
		css = strings.ReplaceAll(css, " "+sep, sep)
	}
	css = strings.ReplaceAll(css, ": ", ":")
	return strings.ReplaceAll(css, ";}", "}")
}

// Footnotes: goldmark's fn:1 ids become fn-1, which EPUB readers and CSS
// selectors take without escaping, and the back links get a spoken name.

var footnoteID = regexp.MustCompile(`^(#?fn(?:ref\d*)?):(\d+)$`)

func footnotes(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		for i, a := range c.Attr {
			if (a.Key == "id" || a.Key == "href") && footnoteID.MatchString(a.Val) {
				c.Attr[i].Val = footnoteID.ReplaceAllString(a.Val, "$1-$2")
			}
		}
		switch {
		case c.Data == "a" && hasClass(c, "footnote-backref"):
			href := attr(c, "href")
			setAttr(c, "aria-label", "Back to reference "+href[strings.LastIndex(href, "-")+1:])
		case c.Data == "a" && hasClass(c, "footnote-ref"):
			setAttr(c, "aria-label", "Note "+textOf(c))
		case c.Data == "div" && hasClass(c, "footnotes"):
			setAttr(c, "aria-label", "Notes")
		}
		footnotes(c)
	}
}

// Callouts: a blockquote opening with [!NOTE], [!TIP], [!IMPORTANT],
// [!WARNING], [!CAUTION], [!VERDICT] or [!SUMMARY] becomes a labelled note.

var calloutMarker = regexp.MustCompile(`(?i)^\s*\[!(note|tip|important|warning|caution|verdict|summary)\][ \t]*\n?`)

func callouts(n *html.Node) {
	each(n, "blockquote", func(quote *html.Node) {
		callouts(quote)
		children := elementChildren(quote)
		if len(children) == 0 || children[0].Data != "p" {
			return
		}
		lead := children[0]
		first := lead.FirstChild
		if first == nil || first.Type != html.TextNode {
			return
		}
		match := calloutMarker.FindStringSubmatch(first.Data)
		if match == nil {
			return
		}
		kind := strings.ToLower(match[1])
		first.Data = strings.TrimLeft(first.Data[len(match[0]):], " \t\n")
		if first.Data == "" {
			lead.RemoveChild(first)
		}
		if strings.TrimSpace(textOf(lead)) == "" && len(elementChildren(lead)) == 0 {
			quote.RemoveChild(lead)
			lead = nil
		}
		box := element("div", "class", "callout", "data-kind", kind, "role", "note")
		quote.Parent.InsertBefore(box, quote)
		quote.Parent.RemoveChild(quote)
		for c := quote.FirstChild; c != nil; {
			next := c.NextSibling
			quote.RemoveChild(c)
			box.AppendChild(c)
			c = next
		}
		label := element("strong", "class", "callout-label")
		label.AppendChild(&html.Node{Type: html.TextNode, Data: strings.ToUpper(kind[:1]) + kind[1:]})
		stop := element("span", "class", "sr-only")
		stop.AppendChild(&html.Node{Type: html.TextNode, Data: ":"})
		label.AppendChild(stop)
		if lead == nil {
			lead = element("p")
			box.InsertBefore(lead, box.FirstChild)
		}
		lead.InsertBefore(&html.Node{Type: html.TextNode, Data: " "}, lead.FirstChild)
		lead.InsertBefore(label, lead.FirstChild)
	})
}

// Figures: a paragraph holding only an image becomes a figure, and the
// image's title becomes its caption. Figures next to each other share a grid.

func figures(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			if c.Data == "p" {
				if figure := imageFigure(c); figure != nil {
					n.InsertBefore(figure, c)
					n.RemoveChild(c)
				}
			} else if c.Data != "figure" && c.Data != "table" && c.Data != "pre" {
				figures(c)
			}
		}
		c = next
	}
	groupFigures(n)
}

func imageFigure(p *html.Node) *html.Node {
	only := onlyChild(p)
	img := only
	if only != nil && only.Data == "a" {
		img = onlyChild(only)
	}
	if img == nil || img.Data != "img" || img.Type != html.ElementNode {
		return nil
	}
	figure := element("figure")
	p.RemoveChild(only)
	figure.AppendChild(only)
	if caption := strings.TrimSpace(attr(img, "title")); caption != "" {
		img.Attr = withoutAttr(img.Attr, "title")
		figcaption := element("figcaption")
		figcaption.AppendChild(&html.Node{Type: html.TextNode, Data: caption})
		figure.AppendChild(figcaption)
	}
	return figure
}

func groupFigures(n *html.Node) {
	var run []*html.Node
	flush := func() {
		if len(run) > 1 {
			grid := element("div", "class", "figure-grid")
			n.InsertBefore(grid, run[0])
			for _, f := range run {
				n.RemoveChild(f)
				grid.AppendChild(f)
			}
		}
		run = nil
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.ElementNode && c.Data == "figure" && firstElement(c, "img") != nil && firstElement(c, "table") == nil:
			run = append(run, c)
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) == "":
		default:
			flush()
		}
		c = next
	}
	flush()
}

// imageLinks lets a reader open each image at full size. The attribute order
// is fixed: the editor preview recognises these links by their exact text.
func imageLinks(n *html.Node, inLink bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			if c.Data == "img" && !inLink {
				if src := attr(c, "src"); src != "" && safeURL(src, false) {
					wrapNode(n, c, element("a", "href", src, "class", "report-image", "aria-label", "Open image at full size"))
				}
			} else {
				imageLinks(c, inLink || c.Data == "a")
			}
		}
		c = next
	}
}

// Tables scroll inside a labelled region named after their caption or the
// heading above them. Columns of numbers align right. In comparisons the first
// column holds row headers.

var numericCell = regexp.MustCompile(`^[+\-−±~≈<>]?\s?[£$€¥₹]?\s?\d[\d.,]*(\s?[-–]\s?[£$€¥₹]?\d[\d.,]*)?\s?(%|[a-zA-Zµ°/²³]{1,8}(\s[a-zA-Z]{1,8})?)?$`)

func tables(root *html.Node, layout string) {
	heading, count := "", 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode {
				switch {
				case headingLevels[c.Data] > 0:
					heading = textOf(c)
				case c.Data == "table":
					count++
					decorateTable(n, c, heading, count, layout)
				default:
					walk(c)
				}
			}
			c = next
		}
	}
	walk(root)
}

func decorateTable(parent, table *html.Node, heading string, count int, layout string) {
	label := heading
	if caption := firstElement(table, "caption"); caption != nil && textOf(caption) != "" {
		label = textOf(caption)
	}
	if label == "" {
		label = "Table " + strconv.Itoa(count)
	}
	rows := tableRows(table)
	numeric := numericColumns(rows)
	widths := columnWidths(rows)
	for _, row := range rows {
		for i, cell := range row.cells {
			switch {
			case numeric[i]:
				if attr(cell, "align") == "" {
					addClass(cell, "num")
				}
			case widths[i] <= 16:
				addClass(cell, "short")
			case widths[i] > 40:
				addClass(cell, "long")
			}
			if row.head && cell.Data == "th" && attr(cell, "scope") == "" {
				setAttr(cell, "scope", "col")
			}
			if row.head {
				keepHyphensWhole(cell)
			}
		}
		if layout == "comparison" && !row.head && len(row.cells) > 1 && !numeric[0] && row.cells[0].Data == "td" {
			row.cells[0].Data, row.cells[0].DataAtom = "th", atom.Th
			setAttr(row.cells[0], "scope", "row")
		}
	}
	frame := element("div", "class", "table-frame")
	scroll := element("div", "class", "table-scroll", "tabindex", "0", "role", "region", "aria-label", label+", table", "data-scroll", "")
	parent.InsertBefore(frame, table)
	parent.RemoveChild(table)
	frame.AppendChild(scroll)
	scroll.AppendChild(table)
}

// hyphenated is a word such as "3-pack" or "Wi-Fi" that a narrow column
// heading should never break after its hyphen.
var hyphenated = regexp.MustCompile(`[\p{L}\p{N}(]+(?:-[\p{L}\p{N})]+)+`)

// keepHyphensWhole wraps hyphenated words in a column heading in
// span.nobr, so "Price (3-pack)" wraps as "Price" over "(3-pack)".
func keepHyphensWhole(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.ElementNode:
			keepHyphensWhole(c)
		case c.Type == html.TextNode && hyphenated.MatchString(c.Data):
			text, at := c.Data, 0
			for _, m := range hyphenated.FindAllStringIndex(text, -1) {
				if m[0] > at {
					n.InsertBefore(&html.Node{Type: html.TextNode, Data: text[at:m[0]]}, c)
				}
				span := element("span", "class", "nobr")
				span.AppendChild(&html.Node{Type: html.TextNode, Data: text[m[0]:m[1]]})
				n.InsertBefore(span, c)
				at = m[1]
			}
			if at < len(text) {
				n.InsertBefore(&html.Node{Type: html.TextNode, Data: text[at:]}, c)
			}
			n.RemoveChild(c)
		}
		c = next
	}
}

type tableRow struct {
	head  bool
	cells []*html.Node
}

func tableRows(table *html.Node) []tableRow {
	var rows []tableRow
	for _, section := range elementChildren(table) {
		var trs []*html.Node
		switch section.Data {
		case "thead", "tbody", "tfoot":
			for _, tr := range elementChildren(section) {
				if tr.Data == "tr" {
					trs = append(trs, tr)
				}
			}
		case "tr":
			trs = append(trs, section)
		}
		for _, tr := range trs {
			row := tableRow{head: section.Data == "thead"}
			for _, cell := range elementChildren(tr) {
				if cell.Data == "td" || cell.Data == "th" {
					row.cells = append(row.cells, cell)
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// numericColumns marks columns whose every filled body cell is a number,
// a sum of money, a percentage or a number with a short unit.
func numericColumns(rows []tableRow) map[int]bool {
	numeric, seen := map[int]bool{}, map[int]bool{}
	for _, row := range rows {
		for _, cell := range row.cells {
			if attr(cell, "colspan") != "" || attr(cell, "rowspan") != "" {
				return map[int]bool{}
			}
		}
	}
	for _, row := range rows {
		if row.head {
			continue
		}
		for i, cell := range row.cells {
			value := textOf(cell)
			if value == "" || value == "-" || value == "–" || value == "—" {
				continue
			}
			ok := len(value) <= 24 && numericCell.MatchString(value)
			if !seen[i] {
				seen[i], numeric[i] = true, ok
			} else {
				numeric[i] = numeric[i] && ok
			}
		}
	}
	return numeric
}

// columnWidths gives each column's longest body cell, in characters.
func columnWidths(rows []tableRow) map[int]int {
	widths := map[int]int{}
	for _, row := range rows {
		if row.head {
			continue
		}
		for i, cell := range row.cells {
			widths[i] = max(widths[i], utf8.RuneCountInString(textOf(cell)))
		}
	}
	return widths
}

// Code blocks get a frame with the fence's language as a label. Prose-like
// languages wrap; code keeps its lines and scrolls.

var codeLanguages = map[string]string{
	"js": "JavaScript", "javascript": "JavaScript", "jsx": "JSX", "ts": "TypeScript", "typescript": "TypeScript", "tsx": "TSX",
	"py": "Python", "python": "Python", "go": "Go", "golang": "Go", "rs": "Rust", "rust": "Rust", "rb": "Ruby", "ruby": "Ruby",
	"sh": "Shell", "bash": "Shell", "zsh": "Shell", "shell": "Shell", "console": "Console", "fish": "Fish", "ps": "PowerShell", "powershell": "PowerShell",
	"json": "JSON", "jsonc": "JSON", "yaml": "YAML", "yml": "YAML", "toml": "TOML", "xml": "XML", "html": "HTML", "css": "CSS", "scss": "SCSS",
	"sql": "SQL", "swift": "Swift", "kotlin": "Kotlin", "kt": "Kotlin", "java": "Java", "c": "C", "cpp": "C++", "c++": "C++", "cs": "C#", "csharp": "C#",
	"php": "PHP", "lua": "Lua", "r": "R", "diff": "Diff", "patch": "Diff", "dockerfile": "Dockerfile", "make": "Makefile", "makefile": "Makefile",
	"ini": "INI", "csv": "CSV", "graphql": "GraphQL", "proto": "Protocol Buffers", "nix": "Nix", "hcl": "HCL", "elixir": "Elixir", "ex": "Elixir",
	"md": "Markdown", "markdown": "Markdown", "text": "Text", "txt": "Text", "plaintext": "Text", "plain": "Text", "log": "Log", "output": "Output",
}

var wrappedLanguages = map[string]bool{"md": true, "markdown": true, "text": true, "txt": true, "plaintext": true, "plain": true, "log": true, "output": true}

func codeBlocks(root *html.Node) {
	each(root, "pre", func(pre *html.Node) {
		code := onlyChild(pre)
		language := ""
		if code != nil && code.Data == "code" {
			for _, class := range strings.Fields(attr(code, "class")) {
				if strings.HasPrefix(class, "language-") {
					language = strings.ToLower(strings.TrimPrefix(class, "language-"))
				}
			}
		}
		if language == "mermaid" || language == "echarts" {
			return
		}
		frame := element("div", "class", "code-block")
		if language != "" {
			name := codeLanguages[language]
			if name == "" {
				name = clip(language, 24)
			}
			setAttr(frame, "data-lang", name)
			setAttr(pre, "aria-label", name+" code")
		} else {
			setAttr(pre, "aria-label", "Code")
		}
		if wrappedLanguages[language] {
			setAttr(frame, "data-wrap", "")
		}
		setAttr(pre, "tabindex", "0")
		setAttr(pre, "role", "group")
		setAttr(pre, "data-scroll", "")
		wrapNode(pre.Parent, pre, frame)
	})
}
