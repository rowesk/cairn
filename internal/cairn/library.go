package cairn

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// pageSize is how many pages the library lists at a time.
const pageSize = 50

// libraryItem is one row of the library list.
type libraryItem struct {
	File                                   bool
	FileName                               string
	FileSize                               int64
	Slug, Title, Agent, CreatedAt, Preview string
	// Asked is the request summary in one line, when the agent sent one.
	Asked                               string
	When, WhenFull                      string
	Link                                string
	Archived, Shared, Protected, Unread bool
	Editable                            bool
	Photo                               string
	TitleHTML, AgentHTML, Snippet       template.HTML
	Where                               string
	// Row numbers the row in the whole list, from 1, for "Show more".
	Row int
	// Access names who can open the page; AccessLong says it in full.
	Access, AccessLong string
}

// libraryQuery is what the library shows: a collection, a search, an agent,
// an order and a window of rows.
type libraryQuery struct {
	Text, Agent, Sort, Kind string
	Archived, Shared        bool
	Offset, Limit           int
}

func readLibraryQuery(v url.Values) libraryQuery {
	q := libraryQuery{Text: strings.TrimSpace(v.Get("q")), Agent: v.Get("agent"), Archived: v.Get("archived") == "1", Limit: pageSize}
	switch v.Get("kind") {
	case "files":
		q.Kind = "file"
	case "links":
		q.Kind = "link"
	}
	q.Shared = v.Get("shared") == "1" && !q.Archived
	if len(q.Text) > 200 {
		q.Text = strings.ToValidUTF8(q.Text[:200], "")
	}
	switch v.Get("sort") {
	case "oldest", "title":
		q.Sort = v.Get("sort")
	case "newest":
		if q.Text != "" {
			q.Sort = "newest"
		}
	}
	// n is how many rows a no-JavaScript "Show more" has asked for.
	if n, err := strconv.Atoi(v.Get("n")); err == nil && n > pageSize {
		q.Limit = min(n, 1000)
	}
	if offset, err := strconv.Atoi(v.Get("offset")); err == nil && offset > 0 {
		q.Offset = offset
	}
	return q
}

// values is the query string that reproduces this view, without its window.
func (q libraryQuery) values() url.Values {
	v := url.Values{}
	if q.Kind == "file" {
		v.Set("kind", "files")
	}
	if q.Kind == "link" {
		v.Set("kind", "links")
	}
	if q.Archived {
		v.Set("archived", "1")
	}
	if q.Shared {
		v.Set("shared", "1")
	}
	if q.Text != "" {
		v.Set("q", q.Text)
	}
	if q.Agent != "" {
		v.Set("agent", q.Agent)
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	return v
}

// href is the library address of this view with extra parameters.
func (q libraryQuery) href(extra url.Values) string {
	v := q.values()
	for key, values := range extra {
		v[key] = values
	}
	if encoded := v.Encode(); encoded != "" {
		return "/?" + encoded
	}
	return "/"
}

type sortOption struct {
	Value, Label string
	Selected     bool
}

type libraryTab struct {
	Name, Href string
	Count      int
	Current    bool
}
type searchHint struct{ Text, Href string }

type libraryViewData struct {
	ownerChrome
	Theme                                string
	Kind, Noun, Nouns                    string
	Tabs                                 []libraryTab
	Hints                                []searchHint
	LibraryHref, SharedHref, ArchiveHref string
	// Sharing is on when the server has a public address.
	Sharing                                 bool
	Query, Agent                            string
	Archived, Shared                        bool
	LibraryCount, SharedCount, ArchiveCount int
	// Total is how many pages match; Items holds the rows shown.
	Total       int
	Items       []libraryItem
	Agents      []string
	Sorts       []sortOption
	CSRF        formToken
	Kindle      bool
	KindleReady bool
	// FirstRun is a library with no pages anywhere yet.
	FirstRun bool
	// MoreHref shows the next rows without JavaScript; MoreOffset is where
	// the next rows start when the list loads them in place.
	MoreHref, ClearHref, Here string
	MoreOffset                int
	// Notice shows beside the row of NoticeSlug, or above the list when it
	// concerns no row on screen.
	Notice     *notice
	NoticeSlug string
	// From follows each page link when the list is searched, filtered or
	// reordered, so the reader's previous and next follow the same list.
	From string
}

// fromQuery is the reader's ?in= parameter that names a library view, or
// nothing for the plain library.
func (q libraryQuery) fromQuery() string {
	// The archive on its own needs nothing: an archived page's neighbours
	// are already the other archived pages.
	if encoded := q.values().Encode(); encoded != "" && encoded != "archived=1" {
		return "?in=" + url.QueryEscape(encoded)
	}
	return ""
}

// CountText says how many pages the list holds, in words.
func (d libraryViewData) CountText() string {
	switch {
	case d.Total == 0:
		return ""
	case len(d.Items) < d.Total:
		return strconv.Itoa(len(d.Items)) + " of " + plural(d.Total, d.Noun, d.Nouns)
	default:
		return plural(d.Total, d.Noun, d.Nouns)
	}
}

// OrderText names the order of the list.
func (d libraryViewData) OrderText() string {
	for _, option := range d.Sorts {
		if option.Selected {
			return option.Label
		}
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := readLibraryQuery(v)
	fragment := v.Get("fragment")
	if fragment != "" && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		s.showError(w, http.StatusForbidden, problemForeignForm)
		return
	}
	if fragment == "rows" {
		q.Limit = pageSize
	} else {
		// The page and the list fragment always start at the top.
		q.Offset = 0
	}
	data := libraryViewData{Kind: q.kind(), Noun: q.kind(), Nouns: q.kind() + "s", Theme: s.themes().Admin, Query: q.Text, Agent: q.Agent, Archived: q.Archived, Shared: q.Shared}
	fail := func() { s.showError(w, http.StatusInternalServerError, problemStorage(r.URL.RequestURI())) }
	items, total, err := s.listPages(q)
	if err != nil {
		fail()
		return
	}
	for i := range items {
		items[i].Row = q.Offset + i + 1
	}
	data.Items, data.Total = items, total
	if err := s.db.QueryRow(`SELECT count(CASE WHEN archived=0 THEN 1 END),count(CASE WHEN archived=0 AND share_enabled=1 THEN 1 END),count(CASE WHEN archived=1 THEN 1 END) FROM pages WHERE kind=?`, q.kind()).Scan(&data.LibraryCount, &data.SharedCount, &data.ArchiveCount); err != nil {
		fail()
		return
	}
	for _, kind := range []string{"page", "file", "link"} {
		tabQuery := q
		tabQuery.Kind, tabQuery.Text, tabQuery.Agent = kind, "", ""
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM pages WHERE kind=? AND archived=? AND (?=0 OR share_enabled=1)`, kind, q.Archived, q.Shared).Scan(&count); err != nil {
			fail()
			return
		}
		data.Tabs = append(data.Tabs, libraryTab{Name: strings.ToUpper(kind[:1]) + kind[1:] + "s", Href: tabQuery.href(nil), Count: count, Current: kind == q.kind()})
	}
	base := libraryQuery{Kind: q.Kind}
	data.LibraryHref = base.href(nil)
	base.Shared = true
	data.SharedHref = base.href(nil)
	base.Shared, base.Archived = false, true
	data.ArchiveHref = base.href(nil)
	var all int
	if err := s.db.QueryRow(`SELECT count(*) FROM pages`).Scan(&all); err != nil {
		fail()
		return
	}
	data.FirstRun = all == 0 && q.kind() == "page"
	if q.Text != "" && total == 0 {
		for _, kind := range []string{"page", "file", "link"} {
			for _, archived := range []bool{false, true} {
				if kind == q.kind() && archived == q.Archived {
					continue
				}
				other := q
				other.Kind, other.Archived, other.Shared, other.Limit = kind, archived, false, 1
				_, count, err := s.listPages(other)
				if err != nil {
					fail()
					return
				}
				if count > 0 {
					one, many := kind, kind+"s"
					if archived {
						one, many = "archived "+one, "archived "+many
					}
					verb := " match."
					if count == 1 {
						verb = " matches."
					}
					data.Hints = append(data.Hints, searchHint{Text: plural(count, one, many) + verb, Href: other.href(nil)})
				}
			}
		}
	}
	if data.Agents, err = s.agentNames(); err != nil {
		fail()
		return
	}
	// An empty order is the natural one: best match while searching,
	// newest first otherwise.
	data.Sorts = []sortOption{{"", "Newest first", false}, {"oldest", "Oldest first", false}, {"title", "Title A to Z", false}}
	if q.Text != "" {
		data.Sorts = []sortOption{{"", "Best match", false}, {"newest", "Newest first", false}, data.Sorts[1], data.Sorts[2]}
	}
	for i := range data.Sorts {
		data.Sorts[i].Selected = data.Sorts[i].Value == q.Sort || (q.Sort == "" && i == 0)
	}
	shown := q.Offset + len(items)
	if shown < total {
		data.MoreOffset = shown
		data.MoreHref = q.href(url.Values{"n": {strconv.Itoa(shown + pageSize)}}) + "#page-row-" + strconv.Itoa(shown+1)
	}
	data.ClearHref = libraryQuery{Kind: q.Kind, Archived: q.Archived, Shared: q.Shared}.href(nil)
	data.From = q.fromQuery()
	data.Here = q.href(nil)
	if q.Limit > pageSize {
		data.Here = q.href(url.Values{"n": {strconv.Itoa(q.Limit)}})
	}
	data.CSRF, data.Kindle, data.KindleReady = s.formToken(), s.cfg.Mail.enabled(), s.kindleAddress() != ""
	data.ownerChrome, data.Sharing = s.chrome(), s.cfg.PublicBaseURL != ""
	switch {
	case v.Has("kindle"):
		// Only a page that exists gets a Try again form; anything else in
		// page could aim the form, and its token, somewhere else.
		slug := s.existingSlug(v.Get("page"))
		data.Notice, data.NoticeSlug = s.kindleNotice(v.Get("kindle"), slug, false), slug
	case v.Get("done") == "archived" || v.Get("done") == "restored":
		data.Notice = s.archiveNotice(v.Get("page"), v.Get("done") == "archived", data.Here)
	case v.Has("deleted"):
		// deleted names a flash, never the title itself, so the address
		// cannot put words of someone else's choosing in the notice.
		text := "Page deleted."
		if title, ok := s.flashes.title(v.Get("deleted")); ok {
			text = "Deleted “" + title + "”."
		}
		if v.Has("cleanup") {
			data.Notice = informed(text + " Some of its files are still on disk; Cairn removes them before the next save or when it restarts.")
		} else {
			data.Notice = succeeded(text)
		}
	}
	listed := false
	for _, item := range data.Items {
		listed = listed || item.Slug == data.NoticeSlug
	}
	if !listed {
		data.NoticeSlug = ""
	}
	privateFormHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; "+uiAssetPolicy+"; img-src 'self' data:; connect-src 'self'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	switch fragment {
	case "list":
		_ = libraryView.ExecuteTemplate(w, "list", data)
	case "rows":
		// The next rows and, when more remain, the next Show more link.
		if libraryView.ExecuteTemplate(w, "rows", data) == nil {
			_ = libraryView.ExecuteTemplate(w, "more", data)
		}
	default:
		_ = libraryView.Execute(w, data)
	}
}

// existingSlug returns slug when it names a page in the library, or "".
func (s *Server) existingSlug(slug string) string {
	if !validSlug(slug) {
		return ""
	}
	p, err := s.getPage(slug)
	if err != nil {
		return ""
	}
	return p.Slug
}

// deletedFlashes remember, for ten minutes, the titles of pages the owner
// just deleted, so the library can name one without the title travelling
// in its address. A restart forgets them; the notice then says "Page
// deleted.".
type deletedFlashes struct {
	mu    sync.Mutex
	items map[string]deletedFlash
}

type deletedFlash struct {
	title   string
	expires time.Time
}

const flashLifetime = 10 * time.Minute

// add keeps a title and returns the id that names it.
func (f *deletedFlashes) add(title string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for id, item := range f.items {
		if now.After(item.expires) {
			delete(f.items, id)
		}
	}
	if len(f.items) >= 64 {
		clear(f.items)
	}
	id := randomID()[:16]
	f.items[id] = deletedFlash{title: title, expires: now.Add(flashLifetime)}
	return id
}

// title finds the title an id names, while it lasts.
func (f *deletedFlashes) title(id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok || time.Now().After(item.expires) {
		return "", false
	}
	return item.title, true
}

// localPath returns raw when it is a path on this site and fallback for
// anything else: another site, a scheme-relative or backslash path, control
// characters, or a value url.Parse cannot read.
func localPath(raw, fallback string) string {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || len(raw) > 2048 {
		return fallback
	}
	if strings.IndexFunc(raw, func(c rune) bool { return c < 0x20 || c == 0x7f || c == '\\' }) >= 0 {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") {
		return fallback
	}
	return raw
}

// archiveNotice confirms an archive or restore made from the list, with a
// one-button Undo that works without JavaScript.
func (s *Server) archiveNotice(slug string, archived bool, here string) *notice {
	if !validSlug(slug) {
		return nil
	}
	p, err := s.getPage(slug)
	if err != nil {
		return nil
	}
	n := succeeded("Restored “" + p.Title + "” to your library.")
	undo := "1"
	if archived {
		n = succeeded("Archived “" + p.Title + "”. Its links still work.")
		undo = "0"
	}
	n.Retry = &noticeRetry{Action: "/_library/archive", Label: "Undo", Fields: map[string]string{"csrf": s.formToken().String(), "slug": slug, "archived": undo, "return": here}}
	return n
}

func (s *Server) agentNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT agent FROM pages ORDER BY agent COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []string
	for rows.Next() {
		var agent string
		if err := rows.Scan(&agent); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

// libraryColumns are the page fields a row needs, read from pages AS p.
const libraryColumns = `p.id,p.slug,p.title,p.agent,p.request_summary,p.original_request,p.created_at,p.preview,p.archived,p.share_enabled,coalesce(p.password_hash,'')!='',p.opened_at,p.source_name,coalesce(p.public_slug,''),p.kind='file',CASE WHEN p.file_name='' THEN p.source_name ELSE p.file_name END,p.file_size,p.object`

// libraryFilter limits a query to the collection and agent; its arguments
// follow from filterArgs.
const libraryFilter = `(?='*' OR p.kind=?) AND p.archived=? AND (?=0 OR p.share_enabled=1) AND (?='' OR p.agent=?)`

func (q libraryQuery) kind() string {
	if q.Kind == "" {
		return "page"
	}
	return q.Kind
}

func (q libraryQuery) filterArgs() []any {
	return []any{q.kind(), q.kind(), q.Archived, q.Shared, q.Agent, q.Agent}
}

func (q libraryQuery) order() string {
	switch q.Sort {
	case "oldest":
		return `p.created_at ASC,p.rowid ASC`
	case "title":
		return `p.title COLLATE NOCASE ASC,p.created_at DESC`
	}
	return `p.created_at DESC,p.rowid DESC`
}

// listPages reads one window of the library. With search text it ranks by
// where the words were found (title, then request, then agent, then the
// page), then by bm25, unless a sort was chosen.
func (s *Server) listPages(q libraryQuery) ([]libraryItem, int, error) {
	if q.Text == "" {
		var total int
		args := q.filterArgs()
		if err := s.db.QueryRow(`SELECT count(*) FROM pages p WHERE `+libraryFilter, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
		items, _, _, err := s.scanItems(`SELECT `+libraryColumns+`,0 FROM pages p WHERE `+libraryFilter+` ORDER BY `+q.order()+` LIMIT ? OFFSET ?`, append(args, q.Limit, q.Offset)...)
		return items, total, err
	}
	terms := searchTerms(q.Text)
	if len(terms) == 0 {
		return nil, 0, nil
	}
	match := ftsQuery(terms)
	var total int
	args := append([]any{match}, q.filterArgs()...)
	if err := s.db.QueryRow(`SELECT count(*) FROM pages_fts f JOIN pages p ON p.id=f.page_id WHERE pages_fts MATCH ? AND `+libraryFilter, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := `m.tier,m.score,p.created_at DESC`
	if q.Sort != "" {
		order = q.order()
	}
	// The tier is the first column holding a match: highlight() differs
	// from the column only where a word matched.
	query := `WITH m AS (SELECT page_id,bm25(pages_fts,` + searchColumnWeights + `) AS score,
 CASE WHEN highlight(pages_fts,1,?,'')!=title THEN 0
      WHEN highlight(pages_fts,2,?,'')!=summary OR highlight(pages_fts,3,?,'')!=request THEN 1
      WHEN highlight(pages_fts,4,?,'')!=agent THEN 2 ELSE 3 END AS tier
 FROM pages_fts WHERE pages_fts MATCH ?)
SELECT ` + libraryColumns + `,m.tier FROM m JOIN pages p ON p.id=m.page_id WHERE ` + libraryFilter + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	queryArgs := append([]any{markOpen, markOpen, markOpen, markOpen, match}, q.filterArgs()...)
	items, tiers, ids, err := s.scanItems(query, append(queryArgs, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	if err := s.highlight(items, tiers, ids, match, terms); err != nil {
		return nil, 0, err
	}
	if s.indexing.Load() && len(items) < q.Limit {
		// Pages the background rebuild has not reached yet still match by
		// title and request, after the indexed ones.
		var waiting int
		fallback := `p.index_version<? AND ` + libraryFilter + ` AND (instr(lower(p.title),lower(?))>0 OR instr(lower(p.original_request),lower(?))>0 OR instr(lower(p.request_summary),lower(?))>0)`
		fallbackArgs := append(append([]any{derivedVersion}, q.filterArgs()...), q.Text, q.Text, q.Text)
		if err := s.db.QueryRow(`SELECT count(*) FROM pages p WHERE `+fallback, fallbackArgs...).Scan(&waiting); err == nil && waiting > 0 {
			more, _, _, err := s.scanItems(`SELECT `+libraryColumns+`,0 FROM pages p WHERE `+fallback+` ORDER BY `+q.order()+` LIMIT ? OFFSET ?`, append(fallbackArgs, q.Limit-len(items), max(0, q.Offset-total))...)
			if err == nil {
				items = append(items, more...)
				total += waiting
			}
		}
	}
	return items, total, nil
}

// scanItems runs a library query and fills in what each row shows. The
// last column is the search tier, returned alongside each page's id.
func (s *Server) scanItems(query string, args ...any) ([]libraryItem, []int, []string, error) {
	photos, err := s.avatarURLs()
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var items []libraryItem
	var tiers []int
	ids := []string{}
	now := time.Now()
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	for rows.Next() {
		var p libraryItem
		var id, summary, request, opened, source, publicSlug, object string
		var tier int
		if err := rows.Scan(&id, &p.Slug, &p.Title, &p.Agent, &summary, &request, &p.CreatedAt, &p.Preview, &p.Archived, &p.Shared, &p.Protected, &opened, &source, &publicSlug, &p.File, &p.FileName, &p.FileSize, &object, &tier); err != nil {
			return nil, nil, nil, err
		}
		if p.File && p.FileSize == 0 {
			if info, err := os.Stat(filepath.Join(s.cfg.DataDir, "objects", object, "source.bin")); err == nil {
				p.FileSize = info.Size()
			}
		}
		p.Photo = photos[p.Agent]
		p.Access, p.AccessLong = "Private", "Private. Only your devices can open it."
		if p.Shared {
			p.Access, p.AccessLong = "Shared", "Shared. Anyone with the link can read it."
			if p.Protected {
				p.AccessLong = "Shared. Anyone with the link and its password can read it."
			}
		}
		p.Unread = opened == ""
		p.Editable = !p.File && editableSource(source)
		p.Asked = askedLine(summary, request)
		p.When, p.WhenFull = relativeDate(p.CreatedAt, now)
		p.Link = "/" + p.Slug
		if p.Shared && base != "" {
			if publicSlug == "" {
				publicSlug = p.Slug
			}
			p.Link = base + "/" + publicSlug
		}
		items = append(items, p)
		tiers = append(tiers, tier)
		ids = append(ids, id)
	}
	return items, tiers, ids, rows.Err()
}

// avatarURLs maps each author with a photo to the address of that photo.
// The address changes with the photo, so browsers keep it for a year.
func (s *Server) avatarURLs() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT name,photo FROM author_profiles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	urls := map[string]string{}
	for rows.Next() {
		var name string
		var photo []byte
		if err := rows.Scan(&name, &photo); err != nil {
			return nil, err
		}
		if len(photo) > 0 {
			sum := sha256.Sum256(photo)
			urls[name] = "/_library/avatar?" + url.Values{"name": {name}, "v": {hex.EncodeToString(sum[:6])}}.Encode()
		}
	}
	return urls, rows.Err()
}

// serveAvatar sends an author's photo for the library list.
func (s *Server) serveAvatar(w http.ResponseWriter, r *http.Request) {
	var photo []byte
	s.mu.RLock()
	err := s.db.QueryRow(`SELECT photo FROM author_profiles WHERE name=?`, r.URL.Query().Get("name")).Scan(&photo)
	s.mu.RUnlock()
	if err != nil || len(photo) == 0 {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(photo)
}

// highlight marks the matched words in each row: in the title or agent when
// they matched there, or in a snippet that says where else the match was.
// When the title and agent hold every word searched for, they explain the
// row. Otherwise the snippet comes from the field, and the stretch of it,
// holding the most of the searched words, the rarest counting first.
func (s *Server) highlight(items []libraryItem, tiers []int, ids []string, match string, terms [][]string) error {
	if len(items) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	// FTS5 picks each field's best 64 words; Cairn picks the snippet in them.
	args := []any{markOpen, markClose, markOpen, markClose, markOpen, markClose, "…", markOpen, markClose, "…", markOpen, markClose, "…", match}
	for _, id := range ids {
		args = append(args, id)
	}
	rarest := s.rarestFirst(terms)
	rows, err := s.db.Query(`SELECT page_id,highlight(pages_fts,1,?,?),highlight(pages_fts,4,?,?),snippet(pages_fts,2,?,?,?,64),snippet(pages_fts,3,?,?,?,64),snippet(pages_fts,5,?,?,?,64) FROM pages_fts WHERE pages_fts MATCH ? AND page_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type marks struct{ title, agent, summary, request, body string }
	found := map[string]marks{}
	for rows.Next() {
		var id string
		var m marks
		if err := rows.Scan(&id, &m.title, &m.agent, &m.summary, &m.request, &m.body); err != nil {
			return err
		}
		found[id] = m
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range items {
		m, ok := found[ids[i]]
		if !ok {
			continue
		}
		if strings.Contains(m.title, markOpen) {
			items[i].TitleHTML = marked(m.title)
		}
		if strings.Contains(m.agent, markOpen) {
			items[i].AgentHTML = marked(m.agent)
		}
		named := termsMarked(m.title+" "+m.agent, terms)
		if (tiers[i] == 0 || tiers[i] == 2) && len(named) == len(terms) {
			continue
		}
		best, bestScore := -1, 0
		var bestText string
		for field, text := range []string{m.summary, m.request, m.body} {
			if !strings.Contains(text, markOpen) {
				continue
			}
			window := 14
			if field == 2 {
				window = 16
			}
			snippet, score := bestSnippet(text, terms, rarest, window)
			if score > bestScore {
				best, bestScore, bestText = field, score, snippet
			}
		}
		if best < 0 {
			continue
		}
		items[i].Where = "In your request"
		if best == 2 {
			items[i].Where = "In the page"
		}
		items[i].Snippet = marked(bestText)
	}
	return nil
}

// rarestFirst orders the searched terms by how few pages hold each, so a
// snippet can prefer the word that singles a page out.
func (s *Server) rarestFirst(terms [][]string) []int {
	order := make([]int, len(terms))
	counts := make([]int, len(terms))
	for i, term := range terms {
		order[i] = i
		if len(terms) > 1 {
			_ = s.db.QueryRow(`SELECT count(*) FROM pages_fts WHERE pages_fts MATCH ?`, ftsQuery([][]string{term})).Scan(&counts[i])
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return counts[order[a]] < counts[order[b]] })
	return order
}

// snippetLead is how much of the text before the first match a snippet
// keeps, in whole words: at most 20 characters, about half the line a phone
// shows. The library shows one line, cut at its end, so this puts the first
// match in the middle of that line at 390px instead of past its end.
const snippetLead = 20

// bestSnippet chooses, within an FTS5 snippet, the stretch of up to window
// words of one block holding the most searched terms, the rarest first, and
// starts it just before its first match. score is 0 when the text holds no
// term. A snippet never runs one block into the next.
func bestSnippet(text string, terms [][]string, rarest []int, window int) (string, int) {
	type word struct {
		text  string
		line  int
		terms map[int]bool
	}
	var words []word
	before, after := strings.HasPrefix(text, "…"), strings.HasSuffix(text, "…")
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(text, "…"), "…"), "\n")
	for li, line := range lines {
		for _, w := range strings.Fields(line) {
			words = append(words, word{text: w, line: li, terms: termsMarked(w, terms)})
		}
	}
	rank := make([]int, len(terms))
	for r, i := range rarest {
		rank[i] = r
	}
	// The end of the window that starts at from: window words, or the end
	// of from's block if that comes first.
	stop := func(from int) int {
		to := from
		for to < len(words) && to-from < window && words[to].line == words[from].line {
			to++
		}
		return to
	}
	score := func(from, to int) int {
		seen := map[int]bool{}
		for _, w := range words[from:to] {
			for t := range w.terms {
				seen[t] = true
			}
		}
		total := len(seen) << 12
		for t := range seen {
			total += 1 << (11 - min(rank[t], 11))
		}
		return total
	}
	best, bestScore := 0, 0
	for from := range words {
		if got := score(from, stop(from)); got > bestScore {
			best, bestScore = from, got
		}
	}
	if bestScore == 0 {
		return "", 0
	}
	line := words[best].line
	lineStart, lineEnd := best, best
	for lineStart > 0 && words[lineStart-1].line == line {
		lineStart--
	}
	for lineEnd < len(words) && words[lineEnd].line == line {
		lineEnd++
	}
	first, last := -1, best
	for i := best; i < stop(best); i++ {
		if len(words[i].terms) > 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	from, lead := first, 0
	for from > lineStart && lead+utf8.RuneCountInString(words[from-1].text)+1 <= snippetLead {
		from--
		lead += utf8.RuneCountInString(words[from].text) + 1
	}
	to := min(lineEnd, max(from+window, last+1))
	kept := make([]string, 0, to-from)
	for _, w := range words[from:to] {
		kept = append(kept, w.text)
	}
	snippet := strings.Join(kept, " ")
	if from > lineStart || (line == 0 && before) {
		snippet = "…" + snippet
	}
	if to < lineEnd || (line == len(lines)-1 && after) {
		snippet += "…"
	}
	return snippet, bestScore
}

// termsMarked says which searched terms the marked words in text match:
// each term's last word matches as a prefix, as the search does, ignoring
// case and the common accents.
func termsMarked(text string, terms [][]string) map[int]bool {
	found := map[int]bool{}
	for rest := text; ; {
		open := strings.Index(rest, markOpen)
		if open < 0 {
			break
		}
		rest = rest[open+len(markOpen):]
		end := strings.Index(rest, markClose)
		if end < 0 {
			break
		}
		token := foldLatin(rest[:end])
		rest = rest[end+len(markClose):]
		for i, term := range terms {
			for j, w := range term {
				w = foldLatin(w)
				if token == w || (j == len(term)-1 && strings.HasPrefix(token, w)) {
					found[i] = true
				}
			}
		}
	}
	return found
}

var latinFold = func() *strings.Replacer {
	groups := map[string]string{
		"a": "àáâãäåāăą", "c": "çćĉċč", "d": "ďđ", "e": "èéêëēĕėęě", "g": "ĝğġģ", "h": "ĥħ", "i": "ìíîïĩīĭįı",
		"j": "ĵ", "k": "ķ", "l": "ĺļľŀł", "n": "ñńņň", "o": "òóôõöøōŏő", "r": "ŕŗř", "s": "śŝşš", "t": "ţťŧ",
		"u": "ùúûüũūŭůűų", "w": "ŵ", "y": "ýÿŷ", "z": "źżž",
	}
	var pairs []string
	for base, accented := range groups {
		for _, r := range accented {
			pairs = append(pairs, string(r), base)
		}
	}
	return strings.NewReplacer(pairs...)
}()

// foldLatin lowers case and drops the accents FTS5 ignores in Latin text.
func foldLatin(s string) string {
	return latinFold.Replace(strings.ToLower(s))
}

// askedLine is what the owner asked, for the list: the request summary,
// which agents write from the owner's side in a few words. A raw request
// reads as instructions to the agent and costs a line without helping
// anyone recognise the page, so rows without a summary show none.
func askedLine(summary, _ string) string {
	return clipWords(strings.Join(strings.Fields(summary), " "), 160)
}

// relativeDate says when a page was saved the way people say it, switching
// to the date after a week. full is the exact date and time for hover and
// screen readers.
func relativeDate(created string, now time.Time) (label, full string) {
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return created, created
	}
	t = t.In(now.Location())
	full = t.Format("2 January 2006 at 15:04")
	age := now.Sub(t)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := int(midnight.Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())).Hours()/24 + 0.5)
	switch {
	case age < time.Minute:
		return "Just now", full
	case age < time.Hour:
		return plural(int(age.Minutes()), "minute ago", "minutes ago"), full
	case days == 0:
		return plural(int(age.Hours()), "hour ago", "hours ago"), full
	case days == 1:
		return "Yesterday", full
	case days < 7:
		return strconv.Itoa(days) + " days ago", full
	case t.Year() == now.Year():
		return t.Format("2 Jan"), full
	default:
		return t.Format("2 Jan 2006"), full
	}
}

// editableSource reports whether a page kept Markdown the editor can open.
func editableSource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// editNote says in one sentence why a page cannot be edited in Cairn.
func (s *Server) editNote(p savedPage) string {
	reason, next := s.editReason(p)
	if reason == "" {
		return ""
	}
	return "Cairn can’t edit this page because " + reason + ". " + next
}

// editReason is why a page cannot be edited and what to do instead, for a
// page whose heading already says that Cairn can't edit it.
func (s *Server) editReason(p savedPage) (reason, next string) {
	if !p.File && editableSource(p.FileName) {
		return "", ""
	}
	if p.FileName != "" {
		return "it came from a " + strings.ToUpper(strings.TrimPrefix(filepath.Ext(p.FileName), ".")) + " file", "Change the original and import it again."
	}
	data, _ := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html"))
	if strings.Contains(string(data), "<main data-template=") {
		return p.Agent + " sent its Markdown without a copy to keep", "Ask " + p.Agent + " to publish a new version."
	}
	return "it’s custom HTML from " + p.Agent, "Ask " + p.Agent + " to publish a new version."
}

// libraryAction answers the library's own requests: the private request
// for the preview, link name checks for the editor, and archive or restore
// with undo. The public listener never routes here.
func (s *Server) libraryAction(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/_library/")
	switch {
	case action == "archive":
		s.archiveFromList(w, r)
	case action == "avatar" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.serveAvatar(w, r)
	case strings.HasPrefix(action, "request/") || action == "link-name":
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, "method not allowed", false)
			return
		}
		// A fetch from Cairn's own pages sends the form token in a header,
		// which another site can neither read nor send.
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("Sec-Fetch-Site") == "same-site" || !s.tokenMatches(r.Header.Get("X-Cairn-Token")) {
			writeAPIError(w, http.StatusForbidden, ErrForbidden, "this request did not come from Cairn", false)
			return
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		if action == "link-name" {
			s.checkLinkName(w, r)
			return
		}
		slug := strings.TrimPrefix(action, "request/")
		if !validSlug(slug) {
			writeAPIError(w, http.StatusNotFound, ErrNotFound, "no such page", false)
			return
		}
		p, err := s.getPage(slug)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, ErrNotFound, "no such page", false)
			return
		}
		writeOwnerJSON(w, map[string]any{"request": p.OriginalRequest, "summary": p.RequestSummary, "source": p.RequestSource, "edit": (!p.File && editableSource(p.FileName))})
	default:
		s.notFound(w, r)
	}
}

// writeOwnerJSON answers one of the library's fetches. Nothing caches it and
// no other origin may load it.
func writeOwnerJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	_ = json.NewEncoder(w).Encode(v)
}

// checkLinkName tells the editor while someone types whether a link name
// works, and the full address it gives. page is the page being edited, whose
// own names never count as taken.
func (s *Server) checkLinkName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	shared := r.URL.Query().Get("access") == "shared"
	except := ""
	if slug := r.URL.Query().Get("page"); validSlug(slug) {
		if p, err := s.getPage(slug); err == nil {
			except = p.ID
		}
	}
	_, problem := s.linkNameProblem(name, except)
	base := "http://" + r.Host
	if shared && s.cfg.PublicBaseURL != "" {
		base = strings.TrimRight(s.cfg.PublicBaseURL, "/")
	}
	writeOwnerJSON(w, map[string]any{"ok": problem == "", "message": problem, "base": base + "/"})
}

// archiveFromList archives or restores a page from the library's row menu
// and returns to the list with an Undo. The list's script asks for JSON.
func (s *Server) archiveFromList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, "POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	parseErr := r.ParseForm()
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	refuse := func(status int, p problem) {
		if wantsJSON {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": p.Message})
			return
		}
		s.showError(w, status, p)
	}
	if !sameOrigin(r) {
		refuse(http.StatusForbidden, problemForeignForm)
		return
	}
	back := localPath(r.FormValue("return"), "/")
	if parseErr != nil {
		refuse(http.StatusBadRequest, problemUnreadable(back))
		return
	}
	if !s.freshToken(r) {
		refuse(http.StatusForbidden, problem{Title: "This page had expired", Message: "Nothing changed. Reload your library and try again.", Action: "Reload your library", Href: back})
		return
	}
	slug := r.FormValue("slug")
	archived := r.FormValue("archived") == "1"
	s.mu.Lock()
	p, err := s.getPage(slug)
	if err == nil {
		_, err = s.db.Exec(`UPDATE pages SET archived=?,updated_at=? WHERE id=?`, archived, nowStamp(), p.ID)
	}
	s.mu.Unlock()
	if err == sql.ErrNoRows || !validSlug(slug) {
		refuse(http.StatusNotFound, problemMissingPage)
		return
	}
	if err != nil {
		refuse(http.StatusInternalServerError, problemNotSaved(back))
		return
	}
	if wantsJSON {
		writeOwnerJSON(w, map[string]any{"ok": true, "title": p.Title, "archived": archived})
		return
	}
	done := "restored"
	if archived {
		done = "archived"
	}
	target, err := url.Parse(back)
	if err != nil {
		target = &url.URL{Path: "/"}
	}
	values := target.Query()
	values.Set("done", done)
	values.Set("page", slug)
	target.RawQuery = values.Encode()
	target.Fragment = ""
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}
func privateFormHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; "+uiAssetPolicy+"; img-src data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}
func (s *Server) manage(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/manage/")
	confirm := strings.HasSuffix(slug, "/delete")
	slug = strings.TrimSuffix(slug, "/delete")
	back := "/manage/" + slug
	if !validSlug(slug) {
		s.notFound(w, r)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.mu.RLock()
		defer s.mu.RUnlock()
		p, err := s.getPage(slug)
		if err == sql.ErrNoRows {
			s.notFound(w, r)
			return
		}
		d, stateErr := s.manageState(p)
		if err != nil || stateErr != nil {
			s.showError(w, http.StatusInternalServerError, problemStorage(r.URL.RequestURI()))
			return
		}
		d.Confirm = confirm
		if done, ok := manageDone[r.URL.Query().Get("done")]; ok {
			d.Notice = succeeded(done.text)
			// Archiving from here can be taken back from the confirmation.
			switch r.URL.Query().Get("done") {
			case "archived":
				d.Notice.Retry = &noticeRetry{Action: back, Label: "Undo", Fields: map[string]string{"csrf": d.CSRF.String(), "action": "restore"}}
			case "restored":
				d.Notice.Retry = &noticeRetry{Action: back, Label: "Undo", Fields: map[string]string{"csrf": d.CSRF.String(), "action": "archive"}}
			}
		}
		s.renderManage(w, d, http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, "GET, HEAD, POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	parseErr := r.ParseForm()
	if !sameOrigin(r) {
		s.showError(w, http.StatusForbidden, problemForeignForm)
		return
	}
	if parseErr != nil {
		s.showError(w, http.StatusBadRequest, problemUnreadable(back))
		return
	}
	action := r.FormValue("action")
	if action == "kindle" && !confirm {
		if !s.freshToken(r) {
			http.Redirect(w, r, kindleReturn(r, slug, "expired"), http.StatusSeeOther)
			return
		}
		s.sendToKindle(w, r, slug)
		return
	}
	if (action == "delete") != confirm {
		// Deleting only happens from its confirmation; other changes never do.
		target := back
		if action == "delete" {
			target += "/delete#delete"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	// bcrypt is slow on purpose, so a new password is hashed before the
	// library lock, never under it.
	var newHash []byte
	var hashErr error
	if (action == "password" || action == "sharing") && !confirm && r.FormValue("password") != "" && passwordProblem(r.FormValue("password")) == "" && s.freshToken(r) {
		newHash, hashErr = bcrypt.GenerateFromPassword([]byte(r.FormValue("password")), bcrypt.DefaultCost)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.getPage(slug)
	if err == sql.ErrNoRows {
		s.notFound(w, r)
		return
	}
	var d manageData
	if err == nil {
		d, err = s.manageState(p)
	}
	if err != nil {
		s.showError(w, http.StatusInternalServerError, problemStorage(back))
		return
	}
	d.Confirm = confirm
	section := manageSections[action]
	if section == "" {
		s.showError(w, http.StatusBadRequest, problem{Title: "Cairn didn’t recognise that change", Message: "Nothing changed. Go back to the page and try again.", Action: "Back to the page", Href: back})
		return
	}
	if !s.freshToken(r) {
		// Cairn's own form with an old token: show it again, input intact.
		if action == "rename" {
			d.Title = r.FormValue("title")
		}
		if (action == "share" || action == "sharing") && r.FormValue("public_slug") != "" {
			d.LinkName = r.FormValue("public_slug")
		}
		d.Notices[section] = informed(manageExpired[section])
		s.renderManage(w, d, http.StatusForbidden)
		return
	}
	reject := func(status int, field, message string) {
		d.Errors[field] = message
		s.renderManage(w, d, status)
	}
	done := ""
	switch action {
	case "rename":
		// The title field wraps; line breaks typed or pasted into it are spaces.
		title := strings.Join(strings.Fields(r.FormValue("title")), " ")
		if problem := titleProblem(title); problem != "" {
			d.Title = r.FormValue("title")
			reject(http.StatusBadRequest, "title", problem)
			return
		}
		err = s.retitle(p, title)
		done = "renamed"
	case "archive", "restore":
		_, err = s.db.Exec(`UPDATE pages SET archived=?,updated_at=? WHERE id=?`, action == "archive", nowStamp(), p.ID)
		done = action + "d"
	case "share":
		if !d.CanShare {
			d.Notices["sharing"] = failed("Public links aren’t set up on this server, so the page stays private.")
			s.renderManage(w, d, http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimSpace(r.FormValue("public_slug"))
		if name == "" {
			name = d.PublicSlug
		}
		if status, problem := s.linkNameProblem(name, p.ID); problem != "" {
			d.LinkName = r.FormValue("public_slug")
			reject(status, "public_slug", problem)
			return
		}
		_, err = s.db.Exec(`UPDATE pages SET share_enabled=1,public_slug=?,share_revision=share_revision+1 WHERE id=?`, name, p.ID)
		switch {
		case !d.Shared:
			done = "shared"
		case name == d.PublicSlug:
			done = "relinked"
		case d.PublicSlug != p.Slug:
			// The page's own path keeps working; a replaced alias does not.
			done = "relinked-old"
		default:
			done = "relinked"
		}
	case "password":
		if !d.CanProtect {
			reject(http.StatusBadRequest, "password", "Passwords need a public address that starts with https://.")
			return
		}
		password := r.FormValue("password")
		if problem := passwordProblem(password); problem != "" {
			reject(http.StatusBadRequest, "password", problem)
			return
		}
		if hashErr != nil || newHash == nil {
			d.Notices["sharing"] = failed("Cairn couldn’t set the password. Try again.")
			s.renderManage(w, d, http.StatusInternalServerError)
			return
		}
		// Cairn checked this password's length, so the mark vouches for it.
		_, err = s.db.Exec(`UPDATE pages SET password_hash=?,password_checked=?,share_revision=share_revision+1 WHERE id=?`, string(newHash), passwordStamp(string(newHash)), p.ID)
		done = "password-set"
		if d.Protected {
			done = "password-changed"
		}
	case "sharing":
		// The sharing section's one Save: its link name and, when one was
		// typed, a new password. Both are checked before either changes.
		if !d.CanShare || !d.Shared {
			d.Notices["sharing"] = failed("This page isn’t shared, so there is no link to change. Turn sharing on first.")
			s.renderManage(w, d, http.StatusConflict)
			return
		}
		name, password := strings.TrimSpace(r.FormValue("public_slug")), r.FormValue("password")
		if name == "" {
			name = d.PublicSlug
		}
		relink := name != d.PublicSlug
		if relink {
			if status, problem := s.linkNameProblem(name, p.ID); problem != "" {
				d.Errors["public_slug"] = problem
				d.LinkName = r.FormValue("public_slug")
				if password != "" && passwordProblem(password) != "" {
					d.Errors["password"] = passwordProblem(password)
				}
				s.renderManage(w, d, status)
				return
			}
		}
		if password != "" {
			problem := passwordProblem(password)
			if !d.CanProtect {
				problem = "Passwords need a public address that starts with https://."
			}
			if problem != "" {
				d.LinkName = r.FormValue("public_slug")
				reject(http.StatusBadRequest, "password", problem)
				return
			}
			if hashErr != nil || newHash == nil {
				d.Notices["sharing"] = failed("Cairn couldn’t set the password. Try again.")
				s.renderManage(w, d, http.StatusInternalServerError)
				return
			}
		}
		tx, txErr := s.db.Begin()
		if err = txErr; err == nil {
			if relink {
				_, err = tx.Exec(`UPDATE pages SET public_slug=?,share_revision=share_revision+1 WHERE id=?`, name, p.ID)
			}
			if err == nil && password != "" {
				_, err = tx.Exec(`UPDATE pages SET password_hash=?,password_checked=?,share_revision=share_revision+1 WHERE id=?`, string(newHash), passwordStamp(string(newHash)), p.ID)
			}
			if err == nil {
				err = tx.Commit()
			} else {
				tx.Rollback()
			}
		}
		switch {
		case relink && password != "":
			done = "relinked-password"
		case relink && d.PublicSlug != p.Slug:
			done = "relinked-old"
		case relink:
			done = "relinked"
		case password != "" && d.Protected:
			done = "password-changed"
		case password != "":
			done = "password-set"
		default:
			done = "unchanged"
		}
	case "remove_password":
		_, err = s.db.Exec(`UPDATE pages SET password_hash=NULL,password_checked='',share_revision=share_revision+1 WHERE id=?`, p.ID)
		done = "password-removed"
	case "unshare":
		_, err = s.db.Exec(`UPDATE pages SET share_enabled=0,share_revision=share_revision+1 WHERE id=?`, p.ID)
		done = "unshared"
	case "delete":
		err = s.deletePage(p)
		if err == nil {
			forgetDraft(w, r, p.ID)
		}
	}
	if err != nil {
		s.showError(w, http.StatusInternalServerError, problemNotSaved(back))
		return
	}
	if action == "delete" {
		target := "/?deleted=" + s.flashes.add(p.Title)
		if s.cleanupPending {
			target += "&cleanup=1"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, back+"?done="+done+"#"+section, http.StatusSeeOther)
}

// retitle renames a page, its index row and the index stamp together.
func (s *Server) retitle(p savedPage, title string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE pages SET title=?,index_source=?,updated_at=? WHERE id=?`, title, indexStamp(p.Object, title, p.RequestSummary, p.OriginalRequest, p.Agent), nowStamp(), p.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE pages_fts SET title=? WHERE page_id=?`, indexable(title), p.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// deletePage removes a page, its index row and its stored replies in one
// transaction, then its files and what the unlock limiter knew of it.
// Callers hold s.mu.
func (s *Server) deletePage(p savedPage) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{`DELETE FROM pages WHERE id=?`, `DELETE FROM pages_fts WHERE page_id=?`, `DELETE FROM idempotency_keys WHERE page_id=?`} {
		if _, err := tx.Exec(query, p.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeObject(p.Object)
	s.unlocks.forget(p.ID)
	return nil
}

// forgetDraft tells the browser that deleted a page to drop that page's
// editor draft: the editor reads the cairn_deleted cookie, removes the
// drafts it names and clears it. It names at most 20 recent pages.
func forgetDraft(w http.ResponseWriter, r *http.Request, id string) {
	ids := []string{id}
	if old, err := r.Cookie("cairn_deleted"); err == nil {
		for _, previous := range strings.Split(old.Value, ".") {
			if len(previous) == 32 && strings.Trim(previous, "0123456789abcdef") == "" && previous != id && len(ids) < 20 {
				ids = append(ids, previous)
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "cairn_deleted", Value: strings.Join(ids, "."), Path: "/", MaxAge: 30 * 24 * 3600, SameSite: http.SameSiteStrictMode})
}

// manageSections places each change's notice in the section it concerns.
var manageSections = map[string]string{
	"rename": "title", "archive": "archive", "restore": "archive", "delete": "delete",
	"share": "sharing", "unshare": "sharing", "password": "sharing", "remove_password": "sharing", "sharing": "sharing",
}

var manageExpired = map[string]string{
	"title":   "This form had expired, so the title wasn’t saved. Check it and save again.",
	"sharing": "This form had expired, so sharing didn’t change. Check it and try again. Enter any password again.",
	"archive": "This form had expired, so nothing changed. Try again.",
	"delete":  "This form had expired, so the page wasn’t deleted. Delete it again if you still want to.",
}

// passwordUnchecked is the manage page's note for a password Cairn never
// measured.
const passwordUnchecked = "This password was set before Cairn asked for 8 characters or more. If it’s shorter, set a new one."

// manageDone confirms a change in place after the redirect back.
var manageDone = map[string]struct{ section, text string }{
	"renamed":           {"title", "Title saved."},
	"archived":          {"archive", "Archived. The page is hidden from your library list, and its links still work."},
	"restored":          {"archive", "Restored. The page is back in your library list."},
	"shared":            {"sharing", "Shared. Anyone with the link can read this page."},
	"relinked":          {"sharing", "Link name saved."},
	"relinked-old":      {"sharing", "Link name saved. The old link no longer works."},
	"relinked-password": {"sharing", "Link name and password saved. Visitors need the new link and the password."},
	"unchanged":         {"sharing", "Nothing to save: the link name is the same and no new password was typed."},
	"unshared":          {"sharing", "Sharing is off. The public link no longer works."},
	"password-set":      {"sharing", "Password set. Visitors need it to read this page."},
	"password-changed":  {"sharing", "Password changed. Visitors need the new one, even if they unlocked the page before."},
	"password-removed":  {"sharing", "Password removed. Anyone with the link can read this page."},
}

type manageData struct {
	ownerChrome
	// Notice confirms the last change, or says why it didn't happen.
	Notice                               *notice
	Theme                                string
	Page                                 savedPage
	Photo                                template.URL
	CSRF                                 formToken
	PublicSlug, PublicURL, PublicHost    string
	Title, LinkName                      string
	Archived, Confirm, Shared, Protected bool
	CanShare, CanProtect                 bool
	// Editable pages kept Markdown the editor can open; EditNote says in
	// one sentence why the others cannot be edited.
	Editable bool
	EditNote string
	// PasswordNote suggests a new password when Cairn never checked the
	// length of the current one: it was set before the 8-character rule,
	// or by an older Cairn.
	PasswordNote string
	Errors       fieldErrors
	Notices      map[string]*notice
}

// manageState reads what the manage page shows. Callers hold s.mu.
func (s *Server) manageState(p savedPage) (manageData, error) {
	d := manageData{Theme: s.themes().Admin, Page: p, Photo: s.authorPhoto(p.Agent), CSRF: s.formToken(), Title: p.Title, CanShare: s.cfg.PublicBaseURL != "", CanProtect: strings.HasPrefix(s.cfg.PublicBaseURL, "https://"), Errors: fieldErrors{}, Notices: map[string]*notice{}}
	var publicSlug sql.NullString
	var hash, checked string
	if err := s.db.QueryRow(`SELECT archived,share_enabled,public_slug,coalesce(password_hash,''),password_checked FROM pages WHERE id=?`, p.ID).Scan(&d.Archived, &d.Shared, &publicSlug, &hash, &checked); err != nil {
		return d, err
	}
	d.Protected = hash != ""
	if d.Protected && checked != passwordStamp(hash) {
		d.PasswordNote = passwordUnchecked
	}
	d.Editable, d.EditNote = (!p.File && editableSource(p.FileName)), s.editNote(p)
	d.PublicSlug = publicSlug.String
	if d.PublicSlug == "" {
		d.PublicSlug = p.Slug
	}
	d.LinkName = d.PublicSlug
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	d.PublicURL = base + "/" + d.PublicSlug
	if u, err := url.Parse(base); err == nil {
		d.PublicHost = u.Host
	}
	return d, nil
}

func (s *Server) renderManage(w http.ResponseWriter, d manageData, status int) {
	// The page has one toast; a section's notice is what it says.
	for _, section := range []string{"title", "sharing", "archive", "delete"} {
		if d.Notice == nil && d.Notices[section] != nil {
			d.Notice = d.Notices[section]
		}
	}
	d.ownerChrome = s.chrome()
	formPageHeaders(w)
	w.WriteHeader(status)
	_ = manageView.Execute(w, d)
}
