package cairn

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// PublicHandler has no private library, metadata or management routes.
func (s *Server) PublicHandler() http.Handler { return http.HandlerFunc(s.servePublic) }
func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !safeHost.MatchString(r.Host) || (r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost) {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_runtime/") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		runtimeHandler.ServeHTTP(w, r)
		return
	}
	if strings.Contains(r.URL.EscapedPath(), "%") {
		http.NotFound(w, r)
		return
	}
	// The public listener serves only shared pages, their content and
	// assets, plus the packaged runtime. Everything else 404s here,
	// including every current and future private route.
	for _, private := range []string{"/api", "/settings", "/manage/", "/new", "/import"} {
		if r.URL.Path == private || strings.HasPrefix(r.URL.Path, strings.TrimSuffix(private, "/")+"/") {
			http.NotFound(w, r)
			return
		}
	}
	if r.URL.Path == "/" {
		http.NotFound(w, r)
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
		http.NotFound(w, r)
		return
	}
	if (r.Method == http.MethodPost && kind != "page") || !validSlug(route) || (kind == "asset" && !assetName.MatchString(asset)) {
		http.NotFound(w, r)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var slug string
	var hash sql.NullString
	var revision int64
	if err := s.db.QueryRow(`SELECT slug,password_hash,share_revision FROM pages WHERE (public_slug=? OR slug=?) AND share_enabled=1 ORDER BY public_slug=? DESC LIMIT 1`, route, route, route).Scan(&slug, &hash, &revision); err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.getPage(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	gateKind := kind
	if r.URL.Query().Get("download") == "1" {
		gateKind = "asset"
	}
	if !s.passwordGate(w, r, p.ID, hash.String, route, gateKind, revision) {
		return
	}
	if r.URL.Query().Get("download") == "1" {
		s.downloadSource(w, r, p)
		return
	}
	if kind == "asset" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		http.ServeFile(w, r, filepath.Join(s.cfg.DataDir, "objects", p.Object, "assets", asset))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if kind == "content" {
		w.Header().Set("Content-Security-Policy", reportPolicy(r.Host, route))
		data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		content := strings.ReplaceAll(string(data), "/_assets/"+p.Slug+"/", "/_assets/"+route+"/")
		content = strings.ReplaceAll(content, "/_content/"+p.Slug+"?", "/_content/"+route+"?")
		w.Write([]byte(reportDocument(content)))
		return
	}
	s.renderReader(w, r, p, route, false, true, hash.String != "", strings.TrimRight(s.cfg.PublicBaseURL, "/")+"/"+route)
}
func reportPolicy(host, slug string) string {
	return "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox; default-src 'none'; script-src 'unsafe-inline' " + host + "/_runtime/; style-src 'unsafe-inline'; img-src " + host + "/_assets/" + slug + "/ " + host + "/_content/" + slug + " data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
}
