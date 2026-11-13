package cairn

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// PublicHandler has no private library, metadata or management routes.
// Its responses are compressed like the private listener's.
func (s *Server) PublicHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logRequests(w, r, "public", func(w http.ResponseWriter, r *http.Request) { compressResponses(w, r, s.servePublic) })
	})
}
func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.publicHosts.allows(r.Host) {
		s.refuseHost(w, r, true)
		return
	}
	r = withHostPolicy(r, s.publicHosts)
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		s.publicNotFound(w, r)
		return
	}
	// Browser and home-screen icons; the app manifest stays private.
	if serveFixedIcon(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_runtime/") {
		serveRuntimeFile(w, r, s.publicNotFound)
		return
	}
	// Status alone, for a proxy or monitor in front of the public listener.
	if r.URL.Path == "/healthz" && r.Method != http.MethodPost {
		s.health(w, r, true)
		return
	}
	// The stylesheet and fonts the password form and public reader need.
	if strings.HasPrefix(r.URL.Path, "/_ui/") {
		serveUI(w, r, s.publicNotFound)
		return
	}
	if strings.Contains(r.URL.EscapedPath(), "%") {
		s.publicNotFound(w, r)
		return
	}
	// The public listener serves only shared pages, their content and
	// assets, plus the packaged runtime. Everything else 404s here,
	// including every current and future private route.
	for _, private := range []string{"/api", "/settings", "/manage/", "/new", "/import"} {
		if r.URL.Path == private || strings.HasPrefix(r.URL.Path, strings.TrimSuffix(private, "/")+"/") {
			s.publicNotFound(w, r)
			return
		}
	}
	if r.URL.Path == "/" {
		s.publicNotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	route := parts[0]
	kind := "page"
	asset := ""
	if len(parts) == 2 && parts[0] == "_content" {
		kind = "content"
		route = parts[1]
		if r.URL.Query().Has("asset") {
			kind = "asset"
			asset = r.URL.Query().Get("asset")
		}
	} else if len(parts) == 3 && parts[0] == "_assets" {
		kind = "asset"
		route = parts[1]
		asset = parts[2]
	} else if len(parts) != 1 {
		s.publicNotFound(w, r)
		return
	}
	ext := ""
	if kind == "page" {
		route, ext = directParts(route)
	}
	if (r.Method == http.MethodPost && kind != "page") || !validSlug(route) || (kind == "asset" && !assetName.MatchString(asset)) {
		s.publicNotFound(w, r)
		return
	}
	s.mu.RLock()
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			s.mu.RUnlock()
		}
	}
	defer unlock()
	var slug string
	var hash sql.NullString
	var revision int64
	err := s.db.QueryRow(`SELECT slug,password_hash,share_revision FROM pages WHERE (public_slug=? OR slug=?) AND share_enabled=1 ORDER BY public_slug=? DESC LIMIT 1`, route, route, route).Scan(&slug, &hash, &revision)
	var p savedPage
	if err == nil {
		p, err = s.getPage(slug)
	}
	if err != nil || (ext != "" && !strings.EqualFold(ext, directExtension(p))) {
		unlock()
		s.publicNotFound(w, r)
		return
	}
	if p.Kind == "link" {
		if kind != "page" || ext != "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			s.publicNotFound(w, r)
			return
		}
		s.redirectShortLink(w, r, p)
		return
	}
	if r.Method == http.MethodPost {
		// Checking a password takes bcrypt's deliberate time; nothing else
		// waits for it.
		unlock()
		s.unlockShare(w, r, p, hash.String, route+ext, revision)
		return
	}
	// Ordinary page sources stay owner-only. A shared file page offers
	// its file, behind the same password gate as the reader.
	if ext == "" && r.URL.Query().Get("download") == "1" && !p.File {
		s.publicNotFound(w, r)
		return
	}
	if !s.passwordGate(w, r, p, hash.String, route, kind, revision) {
		return
	}
	if ext != "" {
		s.serveOriginal(w, r, p, unlock, true)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		s.serveSource(w, r, p, unlock)
		return
	}
	if kind == "asset" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		serveObjectFile(w, r, filepath.Join(s.cfg.DataDir, "objects", p.Object, "assets", asset), s.publicNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if kind == "content" {
		w.Header().Set("Content-Security-Policy", reportPolicy(r.Host, route))
		data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html"))
		if err != nil {
			s.publicNotFound(w, r)
			return
		}
		content := strings.ReplaceAll(string(data), "/_assets/"+p.Slug+"/", "/_assets/"+route+"/")
		content = strings.ReplaceAll(content, "/_content/"+p.Slug+"?", "/_content/"+route+"?")
		w.Write([]byte(reportDocument(reportSchemeOf(r).apply(content))))
		return
	}
	s.renderReader(w, r, p, route, false, true, hash.String != "", strings.TrimRight(s.cfg.PublicBaseURL, "/")+"/"+route)
}
func reportPolicy(host, slug string) string {
	return "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox; default-src 'none'; script-src 'unsafe-inline' " + host + "/_runtime/; style-src 'unsafe-inline'; img-src " + host + "/_assets/" + slug + "/ " + host + "/_content/" + slug + " data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
}
