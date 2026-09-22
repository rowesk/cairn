package cairn

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed ui/reader.html
var readerHTML string

//go:embed ui/reader.js
var readerBodyJS string
var readerJS = focusJS + "\n" + readerBodyJS
var readerHash = func() string {
	sum := sha256.Sum256([]byte(readerJS))
	return base64.StdEncoding.EncodeToString(sum[:])
}()
var readerView = template.Must(template.New("reader").Funcs(uiFunctions).Funcs(template.FuncMap{"readerScript": func() template.JS { return template.JS(readerJS) }}).Parse(readerHTML))

type readerData struct {
	Title, Agent, CreatedAt, Slug, FileName, Request, Link, Format, RequestSource string
	Photo                                                                         template.URL
	Owner, Shared, Protected                                                      bool
}

func (s *Server) renderReader(w http.ResponseWriter, r *http.Request, p savedPage, route string, owner, shared, protected bool, link string) {
	d := readerData{Title: p.Title, Agent: p.Agent, CreatedAt: p.CreatedAt, Slug: route, FileName: p.FileName, Owner: owner, Shared: shared, Protected: protected, Link: link}
	d.Photo = s.authorPhoto(p.Agent)
	d.Format = "HTML"
	if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(p.FileName)), "."); ext != "" {
		d.Format = strings.ToUpper(ext)
		if ext == "md" || ext == "markdown" {
			d.Format = "Markdown"
		}
	}
	if owner {
		d.Request = p.OriginalRequest
		if p.RequestSummary != "" {
			d.Request = p.RequestSummary
		}
		d.RequestSource = p.RequestSource
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-src 'self'; style-src 'unsafe-inline'; font-src data:; img-src data:; script-src 'sha256-"+readerHash+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	_ = readerView.Execute(w, d)
}
func (s *Server) ownerReader(w http.ResponseWriter, r *http.Request, p savedPage) {
	var shared, protected bool
	if err := s.db.QueryRow(`SELECT share_enabled,coalesce(password_hash,'')!='' FROM pages WHERE id=?`, p.ID).Scan(&shared, &protected); err != nil {
		http.Error(w, "page unavailable", 500)
		return
	}
	link := "/" + p.Slug
	if shared {
		link = strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/" + p.Slug
	}
	s.renderReader(w, r, p, p.Slug, true, shared, protected, link)
}
