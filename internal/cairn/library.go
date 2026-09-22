package cairn

import (
	"database/sql"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/html"
)

type libraryItem struct {
	Slug, Title, Agent, OriginalRequest, CreatedAt, Preview string
	Archived, Shared, Protected                             bool
	Photo                                                   template.URL
}
type libraryViewData struct {
	Query, Agent                            string
	Archived, Shared                        bool
	LibraryCount, SharedCount, ArchiveCount int
	Items                                   []libraryItem
	Agents                                  []string
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data := libraryViewData{Query: q.Get("q"), Agent: q.Get("agent"), Archived: q.Get("archived") == "1", Shared: q.Get("shared") == "1" && q.Get("archived") != "1"}
	rows, err := s.db.Query(`SELECT slug,title,agent,coalesce(nullif(request_summary,''),original_request),created_at,preview,archived,share_enabled,coalesce(password_hash,'')!='' FROM pages WHERE archived=? AND (?=0 OR share_enabled=1) AND (?='' OR agent=?) AND (?='' OR instr(lower(title),lower(?))>0 OR instr(lower(original_request),lower(?))>0 OR instr(lower(request_summary),lower(?))>0) ORDER BY created_at DESC,rowid DESC`, data.Archived, data.Shared, data.Agent, data.Agent, data.Query, data.Query, data.Query, data.Query)
	if err != nil {
		http.Error(w, "library unavailable", 500)
		return
	}
	for rows.Next() {
		var p libraryItem
		if err := rows.Scan(&p.Slug, &p.Title, &p.Agent, &p.OriginalRequest, &p.CreatedAt, &p.Preview, &p.Archived, &p.Shared, &p.Protected); err != nil {
			rows.Close()
			http.Error(w, "library unavailable", 500)
			return
		}
		data.Items = append(data.Items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "library unavailable", 500)
		return
	}
	rows, err = s.db.Query(`SELECT DISTINCT agent FROM pages ORDER BY agent`)
	if err != nil {
		http.Error(w, "library unavailable", 500)
		return
	}
	for rows.Next() {
		var agent string
		if err := rows.Scan(&agent); err != nil {
			rows.Close()
			http.Error(w, "library unavailable", 500)
			return
		}
		data.Agents = append(data.Agents, agent)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "library unavailable", 500)
		return
	}
	if err := s.db.QueryRow(`SELECT count(CASE WHEN archived=0 THEN 1 END),count(CASE WHEN archived=0 AND share_enabled=1 THEN 1 END),count(CASE WHEN archived=1 THEN 1 END) FROM pages`).Scan(&data.LibraryCount, &data.SharedCount, &data.ArchiveCount); err != nil {
		http.Error(w, "library unavailable", 500)
		return
	}
	profiles, profileErr := s.profiles()
	if profileErr != nil {
		http.Error(w, "library unavailable", 500)
		return
	}
	photos := map[string]template.URL{}
	for _, profile := range profiles {
		photos[profile.Name] = profile.Photo
	}
	for i := range data.Items {
		data.Items[i].Photo = photos[data.Items[i].Agent]
	}
	privateFormHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src data:; img-src data:; script-src 'sha256-"+libraryScriptHash+"'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	libraryView.Execute(w, data)
}
func privateFormHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src data:; img-src data:; script-src 'sha256-"+focusScriptHash+"'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}
func (s *Server) manage(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/manage/")
	confirm := strings.HasSuffix(slug, "/delete")
	slug = strings.TrimSuffix(slug, "/delete")
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		s.mu.RLock()
		defer s.mu.RUnlock()
		p, err := s.getPage(slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var archived bool
		s.db.QueryRow(`SELECT archived FROM pages WHERE id=?`, p.ID).Scan(&archived)
		privateFormHeaders(w)
		var shared, protected bool
		var publicSlug sql.NullString
		s.db.QueryRow(`SELECT share_enabled,public_slug,coalesce(password_hash,'')!='' FROM pages WHERE id=?`, p.ID).Scan(&shared, &publicSlug, &protected)
		if publicSlug.String == "" {
			publicSlug.String = p.Slug
		}
		manageView.Execute(w, manageData{Page: p, CSRF: s.csrf, Archived: archived, Confirm: confirm, Shared: shared, Protected: protected, PublicSlug: publicSlug.String, PublicURL: strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/" + publicSlug.String, CanShare: s.cfg.PublicBaseURL != ""})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	if !s.ownerRequest(r) {
		http.Error(w, "reload the page and try again", 403)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.getPage(slug)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "storage unavailable", 500)
		return
	}
	if (r.FormValue("action") == "delete") != confirm {
		http.Error(w, "use the deletion confirmation form", 400)
		return
	}
	switch r.FormValue("action") {
	case "rename":
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" || len(title) > 300 {
			http.Error(w, "title must contain 1 to 300 bytes", 400)
			return
		}
		_, err = s.db.Exec(`UPDATE pages SET title=? WHERE id=?`, title, p.ID)
	case "archive", "restore":
		_, err = s.db.Exec(`UPDATE pages SET archived=? WHERE id=?`, r.FormValue("action") == "archive", p.ID)
	case "share":
		if s.cfg.PublicBaseURL == "" {
			http.Error(w, "public serving has not been configured", 503)
			return
		}
		publicSlug := r.FormValue("public_slug")
		if publicSlug == "" {
			if err := s.db.QueryRow(`SELECT coalesce(nullif(public_slug,''),slug) FROM pages WHERE id=?`, p.ID).Scan(&publicSlug); err != nil {
				http.Error(w, "storage unavailable", 500)
				return
			}
		}
		if !validSlug(publicSlug) {
			http.Error(w, "invalid public path", 400)
			return
		}
		var existing string
		lookup := s.db.QueryRow(`SELECT id FROM pages WHERE (public_slug=? OR slug=?) AND id!=?`, publicSlug, publicSlug, p.ID).Scan(&existing)
		if lookup == nil {
			http.Error(w, "public path is already used", 409)
			return
		}
		if lookup != sql.ErrNoRows {
			http.Error(w, "storage unavailable", 500)
			return
		}
		_, err = s.db.Exec(`UPDATE pages SET share_enabled=1,public_slug=?,share_revision=share_revision+1 WHERE id=?`, publicSlug, p.ID)
	case "password":
		if !strings.HasPrefix(s.cfg.PublicBaseURL, "https://") {
			http.Error(w, "password sharing requires an HTTPS public URL", 400)
			return
		}
		password := r.FormValue("password")
		if len(password) == 0 || len(password) > 72 {
			http.Error(w, "password must contain 1 to 72 bytes", 400)
			return
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			http.Error(w, "could not set password", 500)
			return
		}
		_, err = s.db.Exec(`UPDATE pages SET password_hash=?,share_revision=share_revision+1 WHERE id=?`, string(hash), p.ID)
	case "remove_password":
		_, err = s.db.Exec(`UPDATE pages SET password_hash=NULL,share_revision=share_revision+1 WHERE id=?`, p.ID)
	case "unshare":
		_, err = s.db.Exec(`UPDATE pages SET share_enabled=0,share_revision=share_revision+1 WHERE id=?`, p.ID)
	case "delete":
		_, err = s.db.Exec(`DELETE FROM pages WHERE id=?`, p.ID)
		if err == nil {
			s.removeObject(p.Object)
			s.authMu.Lock()
			delete(s.failedUnlock, p.ID)
			s.authMu.Unlock()
		}
	default:
		http.Error(w, "unknown action", 400)
		return
	}
	if err != nil {
		http.Error(w, "could not update page", 500)
		return
	}
	if s.cleanupPending {
		http.Error(w, "page access removed; file cleanup pending, check storage permissions", 503)
		return
	}
	if r.FormValue("action") == "share" || r.FormValue("action") == "unshare" || r.FormValue("action") == "password" || r.FormValue("action") == "remove_password" {
		http.Redirect(w, r, "/manage/"+slug, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func previewText(source string) string {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if text.Len() > 300 {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "head") {
			return
		}
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
			text.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	words := strings.Join(strings.Fields(text.String()), " ")
	if utf8.RuneCountInString(words) > 180 {
		return string([]rune(words)[:180]) + "…"
	}
	return words
}

type manageData struct {
	Page                                           savedPage
	CSRF, PublicSlug, PublicURL                    string
	Archived, Confirm, Shared, Protected, CanShare bool
}

var manageView = template.Must(template.New("manage").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Manage {{.Page.Title}} · Cairn</title><style>` + ownerCSS + `</style><body><main class="form-page"><a href="/">Your library</a>{{if .Confirm}}<h1>Delete {{.Page.Title}}?</h1><p>This removes the page, its images and its links. There is no undo.</p><form class="actions" method="post" action="/manage/{{.Page.Slug}}/delete"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="delete"><button class="danger">Delete permanently</button><a href="/manage/{{.Page.Slug}}">Cancel</a></form>{{else}}<h1>Manage page</h1><p><a href="/{{.Page.Slug}}">Open {{.Page.Title}}</a></p><form class="actions" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="rename"><label>Title <input name="title" value="{{.Page.Title}}" required maxlength="300"></label><button>Rename</button></form><form class="actions" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="{{if .Archived}}restore{{else}}archive{{end}}"><button>{{if .Archived}}Restore to library{{else}}Archive page{{end}}</button></form><section><h2>Public sharing</h2>{{if .Shared}}<p>{{if .Protected}}Password protected.{{else}}Anyone with the link can read this page.{{end}}</p><form class="actions" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="password"><label>Share password <input type="password" name="password" required autocomplete="new-password"></label><button>{{if .Protected}}Change password{{else}}Set password{{end}}</button></form>{{if .Protected}}<form class="actions" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="remove_password"><button>Remove password</button></form>{{end}}{{end}}{{if .CanShare}}{{if .Shared}}<p><a href="{{.PublicURL}}">{{.PublicURL}}</a></p><form class="actions" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="unshare"><button>Turn sharing off</button></form>{{else}}<p>This page is private.</p>{{end}}<form class="actions" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="share"><label>Public URL name <input name="public_slug" value="{{.PublicSlug}}" placeholder="Use this page's path"></label><button>{{if .Shared}}Update public link{{else}}Enable public link{{end}}</button></form>{{else}}<p>Public serving has not been configured.</p>{{end}}</section><a href="/manage/{{.Page.Slug}}/delete">Delete page</a>{{end}}</main>` + focusScriptTag + `</body></html>`))

func (s *Server) backfillPreviews() error {
	rows, err := s.db.Query(`SELECT id,object FROM pages WHERE preview=''`)
	if err != nil {
		return err
	}
	var pages [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return err
		}
		pages = append(pages, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pages {
		data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p[1], "page.html"))
		if err != nil {
			return err
		}
		preview := previewText(string(data))
		if preview == "" {
			preview = "Visual report"
		}
		if _, err := s.db.Exec(`UPDATE pages SET preview=? WHERE id=?`, preview, p[0]); err != nil {
			return err
		}
	}
	return nil
}
