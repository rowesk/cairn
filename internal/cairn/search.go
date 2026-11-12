package cairn

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Search, excerpts and the unread mark.
//
// Every page has three pieces of derived data: the excerpt the library shows
// (pages.preview), a full-text index row (pages_fts) and the name of its kept
// source file (pages.source_name). They are rebuilt from page.html whenever
// derivedVersion grows, in the background, so a large library still starts at
// once. Publishing, replacing, renaming and deleting keep them current.

// derivedVersion names the current excerpt, index and source algorithms.
// Raise it to rebuild them for every page at the next start. Version 3 sets
// blocks apart at both ends and shortens file cards to "PDF · 47 KB".
const derivedVersion = 3

// Snippet markers. They are private-use characters, removed from indexed
// text, so highlighted output can be escaped first and marked up after.
const (
	markOpen  = ""
	markClose = ""
)

// searchColumnWeights rank a title match above the request, the request
// above the agent and the agent above the page's body (bm25 weights, in the
// column order of pages_fts).
const searchColumnWeights = "0, 10.0, 6.0, 5.0, 3.0, 1.0"

// migrateLibrary adds the columns and index this file needs. Callers run it
// once in Open, before serving.
func migrateLibrary(db *sql.DB) error {
	columns := []struct{ name, definition, backfill string }{
		// Pages that existed before unread tracking count as opened. The
		// column and its values arrive together (addColumn), so a power cut
		// can never leave every old page marked unread.
		{"opened_at", "TEXT NOT NULL DEFAULT ''", "UPDATE pages SET opened_at=created_at"},
		{"index_version", "INTEGER NOT NULL DEFAULT 0", ""},
		{"source_name", "TEXT NOT NULL DEFAULT ''", ""},
		// index_source is indexStamp of what the page's index row was built
		// from. An older Cairn that replaces or renames a page leaves it
		// stale, and the next start rebuilds that row (reconcileIndex).
		{"index_source", "TEXT NOT NULL DEFAULT ''", ""},
	}
	for _, column := range columns {
		if err := addColumn(db, "pages", column.name, column.definition, column.backfill); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS pages_fts USING fts5(page_id UNINDEXED, title, summary, request, agent, body, tokenize='unicode61 remove_diacritics 2')`); err != nil {
		return err
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < 1 {
		// The editor and import used to store placeholder words as the
		// owner's request, which the reader then showed as "Your request".
		// Pages the owner wrote themselves have no request. The change and
		// the version that records it commit together.
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE pages SET original_request='' WHERE original_request IN ('Written in Cairn','Imported into Cairn') AND request_summary=''`); err != nil {
			return err
		}
		if _, err := tx.Exec(`PRAGMA user_version=1`); err != nil {
			return err
		}
		return tx.Commit()
	}
	return nil
}

// pageIndex is what the index holds for one page.
type pageIndex struct {
	ID, Title, Summary, Request, Agent, Body string
}

// writeIndex replaces a page's index row inside tx.
func writeIndex(tx *sql.Tx, p pageIndex) error {
	if _, err := tx.Exec(`DELETE FROM pages_fts WHERE page_id=?`, p.ID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO pages_fts(page_id,title,summary,request,agent,body) VALUES(?,?,?,?,?,?)`, p.ID, indexable(p.Title), indexable(p.Summary), indexable(p.Request), indexable(p.Agent), indexable(p.Body))
	return err
}

// indexStamp names what a page's index row was built from: its stored
// object and the fields the index copies. pages.index_source keeps it, so a
// row an older Cairn left behind after a replace or rename shows as stale.
func indexStamp(object, title, summary, request, agent string) string {
	h := sha256.New()
	for _, part := range []string{object, title, summary, request, agent} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// reconcileIndex runs before the background rebuild. It marks for rebuild
// every page whose index row was built from another version of the page,
// and deletes index rows whose page is gone: both happen when an older
// Cairn, which knows nothing of the index, replaced, renamed or deleted
// pages. It works in small steps and stops when Close asks.
func (s *Server) reconcileIndex() error {
	after := ""
	for {
		select {
		case <-s.indexStop:
			return nil
		default:
		}
		done, last, err := s.reconcileStep(after, 200)
		if err != nil {
			return err
		}
		if done {
			break
		}
		after = last
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM pages_fts WHERE page_id NOT IN (SELECT id FROM pages)`)
	return err
}

// reconcileStep checks up to n pages after the id after.
func (s *Server) reconcileStep(after string, n int) (done bool, last string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id,object,title,request_summary,original_request,agent,index_source FROM pages WHERE id>? AND index_version>=? ORDER BY id LIMIT ?`, after, derivedVersion, n)
	if err != nil {
		return false, "", err
	}
	var stale []string
	count := 0
	for rows.Next() {
		var id, object, title, summary, request, agent, stamp string
		if err := rows.Scan(&id, &object, &title, &summary, &request, &agent, &stamp); err != nil {
			rows.Close()
			return false, "", err
		}
		count++
		last = id
		if stamp != indexStamp(object, title, summary, request, agent) {
			stale = append(stale, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, "", err
	}
	for _, id := range stale {
		if _, err := s.db.Exec(`UPDATE pages SET index_version=0 WHERE id=?`, id); err != nil {
			return false, "", err
		}
	}
	return count < n, last, nil
}

// indexable drops the snippet markers from text before it is indexed.
func indexable(s string) string {
	if !strings.ContainsAny(s, markOpen+markClose) {
		return s
	}
	return strings.NewReplacer(markOpen, "", markClose, "").Replace(s)
}

// startIndexing rebuilds derived data for pages saved under an older
// derivedVersion. It runs in the background and stops when Close asks.
func (s *Server) startIndexing() {
	s.indexStop = make(chan struct{})
	s.indexDone = make(chan struct{})
	s.indexing.Store(true)
	go func() {
		defer close(s.indexDone)
		if err := s.reconcileIndex(); err != nil {
			log.Print("Cairn: search index check paused: ", err)
		}
		for {
			select {
			case <-s.indexStop:
				return
			default:
			}
			more, err := s.indexBatch(25)
			if err != nil {
				// Searches keep matching titles and requests of the pages
				// left behind; the next start tries again.
				log.Print("Cairn: search index rebuild paused: ", err)
				return
			}
			if !more {
				s.indexing.Store(false)
				return
			}
		}
	}()
}

// stopIndexing waits for the background rebuild to stop.
func (s *Server) stopIndexing() {
	if s.indexStop == nil {
		return
	}
	select {
	case <-s.indexStop:
	default:
		close(s.indexStop)
	}
	<-s.indexDone
}

// waitIndexed blocks until the background rebuild finishes. Tests use it.
func (s *Server) waitIndexed() {
	if s.indexDone != nil {
		<-s.indexDone
	}
}

// indexBatch rebuilds up to n stale pages and reports whether any remain.
func (s *Server) indexBatch(n int) (bool, error) {
	type stale struct{ id, object, title, summary, request, agent string }
	rows, err := s.db.Query(`SELECT id,object,title,request_summary,original_request,agent FROM pages WHERE index_version<? ORDER BY created_at DESC LIMIT ?`, derivedVersion, n)
	if err != nil {
		return false, err
	}
	var pages []stale
	for rows.Next() {
		var p stale
		if err := rows.Scan(&p.id, &p.object, &p.title, &p.summary, &p.request, &p.agent); err != nil {
			rows.Close()
			return false, err
		}
		pages = append(pages, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(pages) == 0 {
		return false, err
	}
	// Read and parse outside the lock; write only if the page still points
	// at the same object, since a replacement indexes itself.
	type derived struct {
		stale
		preview, body, source string
	}
	var done []derived
	for _, p := range pages {
		d := derived{stale: p}
		dir := filepath.Join(s.cfg.DataDir, "objects", p.object)
		if data, err := os.ReadFile(filepath.Join(dir, "page.html")); err == nil {
			d.preview, d.body = pageText(string(data), p.title)
		}
		d.source = s.sourceName(savedPage{Object: p.object})
		done = append(done, d)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range done {
		tx, err := s.db.Begin()
		if err != nil {
			return false, err
		}
		result, err := tx.Exec(`UPDATE pages SET preview=?,source_name=?,index_version=? WHERE id=? AND object=?`, d.preview, d.source, derivedVersion, d.id, d.object)
		if err == nil {
			if changed, _ := result.RowsAffected(); changed == 1 {
				// Title and request come from the row being updated, so a
				// rename in between keeps its new title.
				var title, summary, request, agent string
				if err = tx.QueryRow(`SELECT title,request_summary,original_request,agent FROM pages WHERE id=?`, d.id).Scan(&title, &summary, &request, &agent); err == nil {
					err = writeIndex(tx, pageIndex{ID: d.id, Title: title, Summary: summary, Request: request, Agent: agent, Body: d.body})
				}
				if err == nil {
					_, err = tx.Exec(`UPDATE pages SET index_source=? WHERE id=?`, indexStamp(d.object, title, summary, request, agent), d.id)
				}
			}
		}
		if err != nil {
			tx.Rollback()
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
	}
	return len(pages) == n, nil
}

// opensPage reports whether a GET is the owner opening a page, rather than
// something another website made the browser fetch, such as an image tag
// pointing at a page. frame is true for the report under /_content/, which
// counts only when Cairn's own pages frame it (the reader and the library's
// preview). The reader itself counts on a top-level navigation. Browsers
// without Fetch Metadata fall back to what a navigation asks for first,
// HTML. Prefetches never count.
func opensPage(r *http.Request, frame bool) bool {
	if r.Method != http.MethodGet || r.Header.Get("Sec-Purpose") != "" || r.Header.Get("Purpose") == "prefetch" {
		return false
	}
	dest, site := r.Header.Get("Sec-Fetch-Dest"), r.Header.Get("Sec-Fetch-Site")
	if frame {
		return dest == "iframe" && site == "same-origin"
	}
	if dest != "" {
		return dest == "document"
	}
	return site == "" && strings.HasPrefix(r.Header.Get("Accept"), "text/html")
}

// markOpened records the first time the owner opens a page, on the private
// listener only. Shared readers never mark a page as read.
func (s *Server) markOpened(id string) {
	_, _ = s.db.Exec(`UPDATE pages SET opened_at=? WHERE id=? AND opened_at=''`, time.Now().UTC().Format(time.RFC3339), id)
}

// searchTerms turns what someone typed into words and quoted phrases. Only
// letters, numbers and marks survive, so FTS syntax characters in the input
// never reach the query as syntax.
func searchTerms(input string) [][]string {
	var terms [][]string
	quoted := false
	var phrase []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			if quoted {
				phrase = append(phrase, word.String())
			} else {
				terms = append(terms, []string{word.String()})
			}
			word.Reset()
		}
	}
	for _, r := range input {
		switch {
		case r == '"' || r == '“' || r == '”':
			flush()
			if quoted && len(phrase) > 0 {
				terms = append(terms, phrase)
			}
			phrase, quoted = nil, !quoted
		case unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r):
			word.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	if quoted && len(phrase) > 0 {
		terms = append(terms, phrase)
	}
	if len(terms) > 12 {
		terms = terms[:12]
	}
	return terms
}

// ftsQuery writes terms as an FTS5 query: every term must match, in any
// column, and the last word of each term matches as a prefix so results
// appear while someone is still typing.
func ftsQuery(terms [][]string) string {
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		parts = append(parts, `"`+strings.Join(term, " ")+`"*`)
	}
	return strings.Join(parts, " ")
}

// matchedLines keeps only the lines of a snippet that hold a match, so a
// snippet never runs a heading into the paragraph after it.
func matchedLines(snippet string) string {
	lines := strings.Split(snippet, "\n")
	var kept []string
	first, last := -1, -1
	for i, line := range lines {
		if strings.Contains(line, markOpen) {
			if first < 0 {
				first = i
			}
			last = i
			kept = append(kept, strings.TrimSpace(line))
		}
	}
	if len(kept) == 0 {
		return strings.Join(strings.Fields(snippet), " ")
	}
	out := strings.Join(kept, " … ")
	if first > 0 && !strings.HasPrefix(out, "…") {
		out = "…" + out
	}
	if last < len(lines)-1 && !strings.HasSuffix(out, "…") {
		out += "…"
	}
	return out
}

// marked escapes highlighted text and turns the markers into <mark>.
func marked(text string) template.HTML {
	escaped := template.HTMLEscapeString(text)
	escaped = strings.ReplaceAll(escaped, markOpen, "<mark>")
	escaped = strings.ReplaceAll(escaped, markClose, "</mark>")
	return template.HTML(escaped)
}

// textSkip are elements whose text is never page text.
var textSkip = map[string]bool{"head": true, "script": true, "style": true, "template": true, "noscript": true, "svg": true, "math": true, "iframe": true, "object": true, "canvas": true}

// excerptSkip are blocks an excerpt never quotes: headings, code, diagrams,
// charts, tables, figures, navigation and footnotes.
var excerptSkip = map[string]bool{"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "pre": true, "table": true, "figure": true, "nav": true, "footer": true, "header": true, "aside": true, "form": true, "button": true, "select": true, "textarea": true, "dialog": true, "summary": true}

// excerptBlocks are the elements that hold readable prose.
var excerptBlocks = map[string]bool{"p": true, "li": true, "dd": true, "blockquote": true}

// pageText reads a stored page and returns its excerpt and its searchable
// body text. The excerpt is the first real paragraph: never the opening
// heading that repeats the title, code, a diagram, a chart, a table, a
// figure, navigation or footnotes. A short first paragraph borrows from the
// next ones.
func pageText(source, title string) (excerpt, body string) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", ""
	}
	body = bodyText(doc)

	// blocks holds readable paragraphs in order; an empty entry marks a
	// heading, so a short opening only borrows from its own section.
	var blocks, code, headings []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type != html.ElementNode && n.Type != html.DocumentNode {
			return
		}
		if n.Type == html.ElementNode {
			if textSkip[n.Data] || hiddenText(n) {
				return
			}
			if hasClass(n, "footnotes") || hasClass(n, "diagram") || hasClass(n, "chart") || hasClass(n, "mermaid") || n.Data == "section" && attr(n, "role") == "doc-endnotes" {
				return
			}
			switch {
			case n.Data == "pre":
				if language := codeLanguage(n); language != "mermaid" && language != "echarts" {
					code = append(code, readable(n))
				}
				blocks = append(blocks, "")
				return
			case len(n.Data) == 2 && n.Data[0] == 'h' && n.Data[1] >= '1' && n.Data[1] <= '6':
				if !hasClass(n, "title-echo") {
					headings = append(headings, readable(n))
				}
				blocks = append(blocks, "")
				return
			case excerptSkip[n.Data]:
				blocks = append(blocks, "")
				return
			case excerptBlocks[n.Data] && !hasBlockChild(n):
				if text := readable(n); text != "" {
					// List items keep a mark, so the excerpt can set them
					// apart rather than running them into one sentence.
					if n.Data == "li" {
						text = listItemMark + text
					}
					blocks = append(blocks, text)
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	folded := strings.ToLower(strings.Join(strings.Fields(title), " "))
	var chosen []string
	length := 0
	for _, block := range blocks {
		if block == "" {
			if len(chosen) > 0 {
				break
			}
			continue
		}
		if strings.ToLower(block) == folded {
			continue
		}
		if len(chosen) == 0 && !strings.HasPrefix(block, listItemMark) && len(strings.Fields(block)) < 4 && len(block) < 30 {
			// A label such as "Updated 3 June" is not the page's opening.
			continue
		}
		chosen = append(chosen, block)
		length += len(block)
		if length >= 90 || len(chosen) == 2 {
			break
		}
	}
	if len(chosen) == 0 {
		for _, block := range blocks {
			if block != "" && strings.ToLower(block) != folded {
				chosen = append(chosen, block)
				break
			}
		}
	}
	if len(chosen) == 0 && len(code) > 0 && code[0] != "" {
		chosen = append(chosen, code[0])
	}
	if len(chosen) == 0 {
		for _, heading := range headings {
			if heading != "" && strings.ToLower(heading) != folded {
				chosen = append(chosen, heading)
				break
			}
		}
	}
	return clipWords(joinExcerpt(chosen), 240), body
}

// listItemMark tags an excerpt block that came from a list item.
const listItemMark = "\x00"

// joinExcerpt runs the chosen blocks together as one line, with a dot
// between list items so "Adapter" never reads as part of the item before.
func joinExcerpt(blocks []string) string {
	var out strings.Builder
	for i, block := range blocks {
		item := strings.HasPrefix(block, listItemMark)
		block = strings.TrimPrefix(block, listItemMark)
		if i > 0 {
			if item {
				out.WriteString(" · ")
			} else {
				out.WriteString(" ")
			}
		}
		out.WriteString(block)
	}
	return out.String()
}

// bodyText is the page's words for the search index, one block per line.
// Table cells are separated with a dot, a diagram keeps only its labels and
// a chart only its words, so snippets read as text rather than syntax.
func bodyText(doc *html.Node) string {
	var all strings.Builder
	cell := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if textSkip[n.Data] {
				return
			}
			if n.Data == "td" || n.Data == "th" {
				cell++
				defer func() { cell-- }()
			}
			if n.Data == "pre" {
				switch codeLanguage(n) {
				case "mermaid":
					all.WriteString(diagramLabels(textOfNode(n)) + "\n")
					return
				case "echarts":
					all.WriteString(chartWords(textOfNode(n)) + "\n")
					return
				}
			}
			// A block starts on a line of its own, so "1,046 kWh<div>gas"
			// never reads as one word.
			if !inlineElement[n.Data] && n.Data != "td" && n.Data != "th" {
				if cell > 0 {
					all.WriteByte(' ')
				} else {
					all.WriteByte('\n')
				}
			}
		}
		if n.Type == html.TextNode {
			// Lines end only where blocks end; source line breaks are spaces.
			all.WriteString(strings.Map(func(r rune) rune {
				if r == '\n' || r == '\r' || r == '\t' {
					return ' '
				}
				return r
			}, n.Data))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "td" || n.Data == "th":
				all.WriteString(" · ")
			case cell > 0:
				// A line break or block inside a cell stays in its row.
				all.WriteByte(' ')
			case !inlineElement[n.Data]:
				all.WriteByte('\n')
			}
		}
	}
	walk(doc)
	var lines []string
	for _, line := range strings.Split(all.String(), "\n") {
		line = strings.Trim(strings.Join(strings.Fields(line), " "), " ·")
		if line != "" {
			lines = append(lines, line)
		}
	}
	body := strings.Join(lines, "\n")
	if len(body) > 1<<20 {
		body = body[:1<<20]
		for !utf8.ValidString(body) {
			body = body[:len(body)-1]
		}
	}
	return body
}

// codeLanguage is the language of a <pre><code class="language-x">.
func codeLanguage(pre *html.Node) string {
	code := firstElement(pre, "code")
	if code == nil {
		return ""
	}
	for _, class := range strings.Fields(attr(code, "class")) {
		if language, ok := strings.CutPrefix(class, "language-"); ok {
			return language
		}
	}
	return ""
}

func textOfNode(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

var diagramLabel = regexp.MustCompile(`\[\[?([^\[\]]+)\]?\]|\(\(?([^()]+)\)?\)|\{\{?([^{}]+)\}?\}|\|([^|]+)\||"([^"]+)"`)

// diagramLabels keeps the words people see in a Mermaid diagram.
func diagramLabels(source string) string {
	var labels []string
	for _, m := range diagramLabel.FindAllStringSubmatch(source, -1) {
		for _, group := range m[1:] {
			if group = strings.TrimSpace(strings.Trim(group, `"()[]{}/\>`)); group != "" {
				labels = append(labels, group)
				break
			}
		}
	}
	return strings.Join(labels, " · ")
}

// chartWords keeps the words in an ECharts option: titles, names and
// category labels, not the settings.
func chartWords(source string) string {
	var value any
	if json.Unmarshal([]byte(source), &value) != nil {
		return ""
	}
	var words []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for key, item := range v {
				if key != "formatter" && key != "type" && key != "position" && !strings.HasSuffix(key, "Color") && key != "color" {
					walk(item)
				}
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		case string:
			if strings.IndexFunc(v, unicode.IsLetter) >= 0 && !strings.ContainsAny(v, "{}#") {
				words = append(words, v)
			}
		}
	}
	walk(value)
	sort.Strings(words)
	return strings.Join(words, " · ")
}

// inlineElement elements continue the current line of text.
var inlineElement = map[string]bool{"a": true, "abbr": true, "b": true, "bdi": true, "bdo": true, "cite": true, "code": true, "data": true, "del": true, "dfn": true, "em": true, "i": true, "ins": true, "kbd": true, "mark": true, "q": true, "s": true, "samp": true, "small": true, "span": true, "strong": true, "sub": true, "sup": true, "time": true, "u": true, "var": true, "wbr": true, "label": true}

// readable is an element's visible text on one line, without footnote
// numbers, screen-reader-only text or callout labels.
func readable(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (textSkip[n.Data] || hiddenText(n) || hasClass(n, "footnote-ref") || hasClass(n, "callout-label") || hasClass(n, "diagram-hint")) {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		// Blocks are set apart at both ends.
		if n.Type == html.ElementNode && !inlineElement[n.Data] {
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && (n.Data == "br" || !inlineElement[n.Data]) {
			b.WriteByte(' ')
		}
	}
	walk(n)
	return squeeze(b.String(), " ")
}

func hiddenText(n *html.Node) bool {
	return hasClass(n, "sr-only") || attr(n, "aria-hidden") == "true" || hasAttr(n, "hidden")
}

func hasBlockChild(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (excerptBlocks[c.Data] || c.Data == "ul" || c.Data == "ol" || c.Data == "div") {
			return true
		}
	}
	return false
}

// squeeze collapses runs of white space to single spaces, keeping one sep
// between lines when sep is a newline.
func squeeze(s, sep string) string {
	if sep == " " {
		return strings.Join(strings.Fields(s), " ")
	}
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, sep)
}

// clipWords shortens text to at most n characters at a word boundary.
func clipWords(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)[:n]
	cut := string(runes)
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-–—") + "…"
}
