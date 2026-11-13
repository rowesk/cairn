package cairn

import (
	_ "embed"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

//go:embed ui/reader.html
var readerHTML string

//go:embed ui/reader.js
var readerBodyJS string
var readerJS = focusJS + "\n" + readerBodyJS
var readerView = pageView("reader", readerHTML, template.FuncMap{
	"icon":        uiIcon,
	"readerTitle": readerTitle,
})

// readerTitle keeps short hyphenated terms such as "27-inch" or "USB-C"
// whole, so a narrow cover never breaks a title inside one.
func readerTitle(title string) template.HTML {
	var b strings.Builder
	for i, word := range strings.Split(title, " ") {
		if i > 0 {
			b.WriteByte(' ')
		}
		inner := strings.Trim(word, "-")
		if strings.Contains(inner, "-") && utf8.RuneCountInString(word) <= 24 && strings.IndexFunc(inner, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			b.WriteString(`<span class="compound">` + template.HTMLEscapeString(word) + `</span>`)
			continue
		}
		b.WriteString(template.HTMLEscapeString(word))
	}
	return template.HTML(b.String())
}

// readerPage is a neighbouring page in the library's default order.
type readerPage struct{ Slug, Title string }

type readerData struct {
	File                                                                  bool
	FileSize                                                              int64
	DirectLink, DirectPath, Preview                                       string
	Theme                                                                 string
	Title, Agent, CreatedAt, Slug, FileName, Request, Link, RequestSource string
	Photo                                                                 template.URL
	CSRF                                                                  formToken
	Owner, Shared, Protected, Kindle, KindleReady                         bool
	Notice                                                                *notice
	Contents                                                              []*contentsEntry
	// Template means Cairn rendered the report, so its text column is the
	// known 43.5rem measure and the cover can line up with it. Layout names
	// the template: report, comparison or visual.
	Template bool
	Layout   string
	// Custom means the report is in its own design (custom HTML): the owner
	// gets a slim neutral strip instead of the Blue Hour cover, so the page
	// owns the canvas.
	Custom bool
	// Rail lets the contents float beside the text when there is room: Cairn
	// template pages with contents, except comparisons, whose tables take the
	// full width. Everywhere else the contents open from the bar.
	Rail bool
	// Download offers the original file: owner only, and only for imported
	// files that are not Markdown the owner can simply edit, nor a document
	// whose file card already has a Download button.
	Download bool
	// ReportLimit is the tallest the report could plausibly be, in CSS
	// pixels. A report that claims more keeps its own scroll instead of
	// stretching the reader.
	ReportLimit int
	// ShortcutsOff turns the single-key shortcuts off (Settings), so they
	// never take a key the owner meant for something else.
	ShortcutsOff bool
	// OwnTitle means the report opens with a title the reader can see, so
	// the cover keeps the page title for screen readers only.
	OwnTitle bool
	// LongTitle steps the cover title down a size.
	LongTitle bool
	// FrameSrc is the report's address, asking for the theme's colours.
	FrameSrc template.URL
	// FixedReport means the report keeps its own colours in every theme:
	// custom HTML, and pages saved before reports followed the theme.
	FixedReport bool
	// Owner only: the editor can open the page, it sits in the archive, and
	// its neighbours in the library list.
	Editable, Archived bool
	Previous, Next     *readerPage
	// From carries the library view the owner came from to the neighbours'
	// links; BackHref returns to it.
	From, BackHref string
}

// followLibraryView makes previous and next follow the library view the
// owner opened the page from (a search, an author, an order) rather than the
// whole library. A page that is no longer in that view falls back to the
// library's order.
func (s *Server) followLibraryView(d *readerData, p savedPage, in string) {
	if in == "" || len(in) > 600 {
		return
	}
	v, err := url.ParseQuery(in)
	if err != nil {
		return
	}
	q := readLibraryQuery(v)
	q.Kind = p.Kind
	q.Offset, q.Limit = 0, 1000
	if q.fromQuery() == "" {
		return
	}
	items, _, err := s.listPages(q)
	if err != nil {
		return
	}
	for i, item := range items {
		if item.Slug != p.Slug {
			continue
		}
		d.Previous, d.Next = nil, nil
		if i > 0 {
			d.Previous = &readerPage{Slug: items[i-1].Slug, Title: items[i-1].Title}
		}
		if i+1 < len(items) {
			d.Next = &readerPage{Slug: items[i+1].Slug, Title: items[i+1].Title}
		}
		d.From = q.fromQuery()
		d.BackHref = q.href(nil)
		return
	}
}

func (s *Server) renderReader(w http.ResponseWriter, r *http.Request, p savedPage, route string, owner, shared, protected bool, link string) {
	d := readerData{Title: p.Title, Agent: p.Agent, CreatedAt: p.CreatedAt, Slug: route, FileName: p.FileName, Owner: owner, Shared: shared, Protected: protected, Link: link, File: p.File && p.FileName != "", FileSize: p.FileSize}
	if ext := directExtension(p); d.File && ext != "" {
		d.DirectLink, d.DirectPath = link+ext, "/"+route+ext
		d.Preview = filePreview(p.FileName)
	}
	themes := s.themes()
	d.Theme = themes.public()
	if owner {
		d.Theme = themes.reader()
	}
	d.Photo = s.authorPhoto(p.Agent)
	d.LongTitle = utf8.RuneCountInString(p.Title) > 80
	d.FrameSrc = template.URL("/_content/" + route + reportSchemeFor(d.Theme).query())
	d.FixedReport = true
	if data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html")); err == nil && !d.File {
		d.FixedReport = !adaptsToScheme(string(data))
		_, headings := reportHeadings(string(data))
		d.Contents = reportContents(headings, route)
		d.Layout, d.Template = reportLayout(string(data))
		d.ReportLimit = reportHeightLimit(string(data))
		d.OwnTitle = reportShowsTitle(headings, p.Title, d.Template)
	}
	d.Custom = !d.Template && !d.File
	d.Rail = len(d.Contents) > 0 && d.Template && d.Layout != "comparison"
	if owner {
		d.Request = strings.TrimSpace(p.OriginalRequest)
		if summary := strings.TrimSpace(p.RequestSummary); summary != "" {
			d.Request = summary
		}
		d.RequestSource = p.RequestSource
		d.CSRF, d.Kindle, d.KindleReady = s.formToken(), s.cfg.Mail.enabled() && !p.File, s.kindleAddress() != ""
		d.Notice = s.kindleNotice(r.URL.Query().Get("kindle"), p.Slug, true)
		if d.Notice == nil {
			d.Notice = savedNotice(r)
		}
		d.ShortcutsOff = !s.preferences().Shortcuts
		d.Editable = !p.File && editableSource(p.FileName)
		// File pages offer Download in place of the content frame.
		d.Download = p.FileName != "" && !d.Editable && !p.File
		d.Archived, d.Previous, d.Next = s.readerNeighbours(p)
		d.BackHref = (libraryQuery{Kind: p.Kind}).href(nil)
		if d.Archived {
			d.BackHref = (libraryQuery{Kind: p.Kind, Archived: true}).href(nil)
		}
		s.followLibraryView(&d, p, r.URL.Query().Get("in"))
	}
	formAction := "'none'"
	if owner {
		// The end of the page archives and sends to Kindle with real form
		// posts. Browsers send Origin: null under no-referrer, which fails
		// the owner check, so owner pages keep the referrer on this origin.
		formAction = "'self'"
		w.Header().Set("Referrer-Policy", "same-origin")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	filePolicy := "img-src data:;"
	if d.File {
		filePolicy = "img-src 'self' data:; media-src 'self';"
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-src 'self'; "+uiAssetPolicy+"; "+filePolicy+" base-uri 'none'; form-action "+formAction+"; frame-ancestors 'none'")
	_ = readerView.Execute(w, d)
}

// reportLayout reads the template a report was rendered with from its
// <main data-template="…">. Custom HTML has none.
func reportLayout(doc string) (layout string, template bool) {
	const marker = `<main data-template="`
	i := strings.Index(doc, marker)
	if i < 0 {
		return "", false
	}
	rest := doc[i+len(marker):]
	if end := strings.IndexByte(rest, '"'); end >= 0 && end <= 32 {
		layout = rest[:end]
	}
	return layout, true
}

// reportHeightLimit is a generous upper bound on how tall a report's page
// can be at the narrowest screen Cairn supports (320px), from what it holds:
// its words, its blocks and its media. Real pages measure well under it; a
// report whose script claims more is not believed (reader.js).
func reportHeightLimit(doc string) int {
	const most = 200000
	words, blocks, media, skip := 0, 0, 0, 0
	z := html.NewTokenizer(strings.NewReader(doc))
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return most
			}
			return min(most, 8000+3*words+120*blocks+1200*media)
		case html.TextToken:
			if skip == 0 {
				words += utf8.RuneCount(z.Text())
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); skip > 0 && (string(name) == "style" || string(name) == "script" || string(name) == "template") {
				skip--
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			switch string(name) {
			case "style", "script", "template":
				skip++
			case "img", "svg", "canvas", "video", "figure", "iframe", "object", "embed", "picture":
				media++
			case "p", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "div", "section", "article", "br", "hr", "pre", "blockquote", "dt", "dd", "table", "details":
				blocks++
			case "code":
				for more {
					var key, value []byte
					key, value, more = z.TagAttr()
					if string(key) == "class" && (strings.Contains(string(value), "language-mermaid") || strings.Contains(string(value), "language-echarts")) {
						media++
					}
				}
			}
		}
	}
}

// reportShowsTitle reports whether the report opens with a title the reader
// can see. Cairn's templates hide an opening h1 that repeats the page title
// (title-echo); pages saved before that rule, and custom HTML, show theirs.
func reportShowsTitle(headings []outlineHeading, title string, template bool) bool {
	if len(headings) == 0 || headings[0].Level != 1 || headings[0].Title {
		return false
	}
	return !template || echoesTitle(headings[0].Text, title)
}

// readerNeighbours finds the pages either side of p in the library's default
// order (newest first) within its own collection, so an archived page only
// ever leads to other archived pages.
func (s *Server) readerNeighbours(p savedPage) (archived bool, previous, next *readerPage) {
	var rowid int64
	if err := s.db.QueryRow(`SELECT archived,rowid FROM pages WHERE id=?`, p.ID).Scan(&archived, &rowid); err != nil {
		return false, nil, nil
	}
	find := func(query string) *readerPage {
		var n readerPage
		if s.db.QueryRow(query, p.Kind, archived, p.CreatedAt, p.CreatedAt, rowid).Scan(&n.Slug, &n.Title) != nil {
			return nil
		}
		return &n
	}
	previous = find(`SELECT slug,title FROM pages WHERE kind=? AND archived=? AND (created_at>? OR (created_at=? AND rowid>?)) ORDER BY created_at ASC,rowid ASC LIMIT 1`)
	next = find(`SELECT slug,title FROM pages WHERE kind=? AND archived=? AND (created_at<? OR (created_at=? AND rowid<?)) ORDER BY created_at DESC,rowid DESC LIMIT 1`)
	return archived, previous, next
}

func (s *Server) ownerReader(w http.ResponseWriter, r *http.Request, p savedPage) {
	var shared, protected bool
	var name string
	if err := s.db.QueryRow(`SELECT share_enabled,coalesce(password_hash,'')!='',coalesce(public_slug,'') FROM pages WHERE id=?`, p.ID).Scan(&shared, &protected, &name); err != nil {
		s.showError(w, http.StatusInternalServerError, problemStorage(r.URL.RequestURI()))
		return
	}
	link := "/" + p.Slug
	if shared {
		// The address the owner named in Manage, which visitors also see.
		if !validSlug(name) {
			name = p.Slug
		}
		link = strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/" + name
	}
	s.renderReader(w, r, p, p.Slug, true, shared, protected, link)
}
