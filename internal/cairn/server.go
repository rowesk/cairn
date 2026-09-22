// Package cairn serves a private library of saved reports.
package cairn

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Config struct{ DataDir, PublisherToken, PublicBaseURL, Owner string }

// APIVersion is served by the private GET /api index.
const APIVersion = "1"

type Server struct {
	shareKey       []byte
	authMu         sync.Mutex
	failedUnlock   map[string]time.Time
	csrf           string
	cleanupPending bool
	lock           *os.File
	cfg            Config
	db             *sql.DB
	mu             sync.RWMutex
}
type publication struct {
	Share           bool              `json:"-"`
	PublicSlug      string            `json:"-"`
	Source          *sourceFile       `json:"-"`
	Capabilities    []string          `json:"capabilities"`
	Markdown        string            `json:"markdown"`
	Template        string            `json:"template"`
	Assets          map[string][]byte `json:"assets"`
	Title           string            `json:"title"`
	Agent           string            `json:"agent"`
	OriginalRequest string            `json:"original_request"`
	RequestSummary  string            `json:"request_summary"`
	RequestSource   string            `json:"request_source"`
	HTML            string            `json:"html"`
	Slug            string            `json:"slug"`
}
type savedPage struct {
	ID, Slug, Title, Agent, OriginalRequest, CreatedAt, Object string
	RequestSummary, RequestSource                              string
	FileName                                                   string
}

func Open(cfg Config) (*Server, error) {
	cfg.Owner = strings.TrimSpace(cfg.Owner)
	if cfg.Owner == "" {
		cfg.Owner = "Owner"
	}
	if len(cfg.Owner) > 100 || !utf8.ValidString(cfg.Owner) {
		return nil, fmt.Errorf("owner name must fit 100 UTF-8 bytes")
	}

	if cfg.PublicBaseURL != "" {
		u, err := url.Parse(cfg.PublicBaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("public base URL must be an HTTP(S) origin")
		}
	}
	if len(cfg.PublisherToken) < 32 {
		return nil, fmt.Errorf("publisher token must have at least 32 characters")
	}
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "objects"), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(cfg.DataDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("data directory is already in use: %w", err)
	}
	opened := false
	defer func() {
		if !opened {
			lock.Close()
		}
	}()
	db, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "cairn.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS pages (
 id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, title TEXT NOT NULL,
 agent TEXT NOT NULL, original_request TEXT NOT NULL, created_at TEXT NOT NULL,
 object TEXT NOT NULL UNIQUE, archived INTEGER NOT NULL DEFAULT 0,
 share_enabled INTEGER NOT NULL DEFAULT 0, public_slug TEXT UNIQUE, password_hash TEXT);
 CREATE TABLE IF NOT EXISTS preferences (id INTEGER PRIMARY KEY CHECK(id=1),author TEXT NOT NULL,template TEXT NOT NULL,link_length INTEGER NOT NULL CHECK(link_length BETWEEN 3 AND 7));

 CREATE TABLE IF NOT EXISTS agent_keys (name TEXT PRIMARY KEY, digest BLOB UNIQUE, created_at TEXT NOT NULL, last_used TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS author_profiles (name TEXT PRIMARY KEY,photo BLOB NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO preferences VALUES(1,?,'report',3)`, cfg.Owner); err != nil {
		db.Close()
		return nil, err
	}
	for _, column := range []string{"request_summary", "request_source"} {
		if _, err := db.Exec("SELECT " + column + " FROM pages LIMIT 0"); err != nil {
			if _, err := db.Exec("ALTER TABLE pages ADD COLUMN " + column + " TEXT NOT NULL DEFAULT ''"); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	if err := migrateLinkPreferences(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`SELECT preview FROM pages LIMIT 0`); err != nil {
		if _, err = db.Exec(`ALTER TABLE pages ADD COLUMN preview TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := os.Chmod(filepath.Join(cfg.DataDir, "cairn.sqlite"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	server := &Server{cfg: cfg, db: db, lock: lock, csrf: randomID(), failedUnlock: map[string]time.Time{}}
	if _, err := db.Exec(`SELECT share_revision FROM pages LIMIT 0`); err != nil {
		if _, err = db.Exec(`ALTER TABLE pages ADD COLUMN share_revision INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := server.loadShareKey(); err != nil {
		db.Close()
		return nil, err
	}
	if err := server.removeUnreferencedObjects(); err != nil {
		db.Close()
		return nil, err
	}
	if err := server.backfillPreviews(); err != nil {
		db.Close()
		return nil, err
	}
	opened = true
	return server, nil
}
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Close()
	lockErr := s.lock.Close()
	if err != nil {
		return err
	}
	return lockErr
}
func randomID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !safeHost.MatchString(r.Host) {
		http.Error(w, "invalid host", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/_runtime/") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		runtimeHandler.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/settings" {
		s.settings(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/manage/") {
		s.manage(w, r)
		return
	}
	if r.URL.Path == "/new" {
		s.newPage(w, r)
		return
	}
	if r.URL.Path == "/import" {
		s.importPage(w, r)
		return
	}
	if (r.URL.Path == "/api/pages" && r.Method == http.MethodPost) || (strings.HasPrefix(r.URL.Path, "/api/pages/") && r.Method == http.MethodPut) {
		s.publish(w, r)
		return
	}
	if r.URL.Path == "/api" {
		s.apiIndex(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost && r.Method != http.MethodPut {
			writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, "method not allowed", false)
		} else {
			writeAPIError(w, http.StatusNotFound, ErrNotFound, "no such API route", false)
		}
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r.URL.Path == "/" {
		s.library(w, r)
		return
	}

	if strings.Contains(r.URL.EscapedPath(), "%") {
		http.NotFound(w, r)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/")
	if strings.HasPrefix(slug, "_assets/") {
		parts := strings.Split(slug, "/")
		if len(parts) != 3 || !validSlug(parts[1]) || !assetName.MatchString(parts[2]) {
			http.NotFound(w, r)
			return
		}
		p, err := s.getPage(parts[1])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		http.ServeFile(w, r, filepath.Join(s.cfg.DataDir, "objects", p.Object, "assets", parts[2]))
		return
	}
	content := strings.HasPrefix(slug, "_content/")
	if content {
		slug = strings.TrimPrefix(slug, "_content/")
	}
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	p, err := s.getPage(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		s.downloadSource(w, r, p)
		return
	}
	if content && r.URL.Query().Has("asset") {
		name := r.URL.Query().Get("asset")
		if !assetName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		http.ServeFile(w, r, filepath.Join(s.cfg.DataDir, "objects", p.Object, "assets", name))
		return
	}
	if content {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", reportPolicy(r.Host, p.Slug))
		data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(reportDocument(string(data))))
		return
	}
	s.ownerReader(w, r, p)
}
func (s *Server) getPage(slug string) (savedPage, error) {
	var p savedPage
	err := s.db.QueryRow(`SELECT id,slug,title,agent,original_request,created_at,object,request_summary,request_source FROM pages WHERE slug=?`, slug).Scan(&p.ID, &p.Slug, &p.Title, &p.Agent, &p.OriginalRequest, &p.CreatedAt, &p.Object, &p.RequestSummary, &p.RequestSource)
	if err == nil {
		p.FileName = s.sourceName(p)
	}
	return p, err
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "none") {
		writeAPIError(w, http.StatusForbidden, ErrBrowserOriginForbidden, "browser publication is not permitted", false)
		return
	}
	author, authenticated := s.publisherIdentity(r)
	if !authenticated {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cairn"`)
		writeAPIError(w, http.StatusUnauthorized, ErrAuthenticationRequired, "publisher credential required", false)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, ErrUnsupportedMediaType, "application/json required", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var in publication
	if err := decoder.Decode(&in); err != nil {
		if isPayloadTooLarge(err) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, ErrPayloadTooLarge, "publication exceeds the 16 MiB request limit", false)
		} else {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidJSON, "invalid publication JSON", false)
		}
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if isPayloadTooLarge(err) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, ErrPayloadTooLarge, "publication exceeds the 16 MiB request limit", false)
		} else {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidJSON, "expected one publication", false)
		}
		return
	}
	if author != "" {
		in.Agent = author
		r = r.WithContext(context.WithValue(r.Context(), publisherAuthorKey{}, author))
	}
	s.savePublication(w, r, in)
}
func (s *Server) savePublication(w http.ResponseWriter, r *http.Request, in publication) {
	var err error
	if err := preparePublication(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrValidationFailed, err.Error(), false)
		return
	}
	if err := addRuntime(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrValidationFailed, err.Error(), false)
		return
	}
	if err := validatePublication(in); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrValidationFailed, err.Error(), false)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanupPending {
		if err := s.removeUnreferencedObjects(); err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, ErrCleanupPending, "pending storage cleanup failed; check data-directory permissions", true)
			return
		}
		s.cleanupPending = false
	}
	replace := r.Method == http.MethodPut
	var previous savedPage
	if replace {
		slug := strings.TrimPrefix(r.URL.Path, "/api/pages/")
		if !validSlug(slug) {
			writeAPIError(w, http.StatusNotFound, ErrNotFound, "no such page", false)
			return
		}
		previous, err = s.getPage(slug)
		if err != nil {
			if err == sql.ErrNoRows {
				writeAPIError(w, http.StatusNotFound, ErrNotFound, "no such page", false)
			} else {
				writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "storage unavailable", true)
			}
			return
		}
		if author, _ := r.Context().Value(publisherAuthorKey{}).(string); author != "" && previous.Agent != author {
			writeAPIError(w, http.StatusForbidden, ErrForbidden, "this key can only replace its own author’s pages", false)
			return
		}
		if in.Slug != "" && in.Slug != slug {
			writeAPIErrorField(w, http.StatusBadRequest, ErrValidationFailed, "slug", "replacement cannot change slug", false)
			return
		}
		in.Slug = slug
	} else {
		if in.Slug == "" {
			in.Slug, err = s.randomSlug()
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, ErrLinkAllocationFailed, "could not create a link", true)
				return
			}
		}
		taken, lookupErr := s.slugTaken(in.Slug)
		if lookupErr != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "storage unavailable", true)
			return
		}
		if taken {
			writeAPIErrorField(w, http.StatusConflict, ErrSlugConflict, "slug", "slug already exists; use explicit replacement", false)
			return
		}
	}
	if in.Share {
		if replace || s.cfg.PublicBaseURL == "" {
			writeAPIError(w, http.StatusServiceUnavailable, ErrSharingUnavailable, "public sharing is not configured yet", true)
			return
		}
		if in.PublicSlug == "" {
			in.PublicSlug = in.Slug
		}
		if !validSlug(in.PublicSlug) {
			writeAPIErrorField(w, http.StatusBadRequest, ErrValidationFailed, "public_slug", "invalid public shortlink", false)
			return
		}
		var existing string
		if e := s.db.QueryRow(`SELECT id FROM pages WHERE public_slug=? OR slug=?`, in.PublicSlug, in.PublicSlug).Scan(&existing); e != sql.ErrNoRows {
			writeAPIErrorField(w, http.StatusConflict, ErrPublicSlugUnavailable, "public_slug", "public shortlink is unavailable", false)
			return
		}
	}
	id := previous.ID
	created := previous.CreatedAt
	if !replace {
		id = randomID()
		created = time.Now().UTC().Format(time.RFC3339)
	}
	if replace {
		if in.RequestSummary == "" {
			in.RequestSummary = previous.RequestSummary
		}
		if in.RequestSource == "" {
			in.RequestSource = previous.RequestSource
		}
	}
	object, err := s.writeObject(in)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "storage unavailable", true)
		return
	}
	if replace {
		_, err = s.db.Exec(`UPDATE pages SET title=?,agent=?,original_request=?,object=?,preview=?,request_summary=?,request_source=? WHERE id=?`, in.Title, in.Agent, in.OriginalRequest, object, previewText(in.HTML), in.RequestSummary, in.RequestSource, id)
	} else {
		_, err = s.db.Exec(`INSERT INTO pages (id,slug,title,agent,original_request,created_at,object,preview,request_summary,request_source,share_enabled,public_slug) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, id, in.Slug, in.Title, in.Agent, in.OriginalRequest, created, object, previewText(in.HTML), in.RequestSummary, in.RequestSource, in.Share, func() any {
			if in.Share {
				return in.PublicSlug
			}
			return nil
		}())
	}
	if err != nil {
		s.removeObject(object)
		writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "could not save page", true)
		return
	}
	if replace {
		s.removeObject(previous.Object)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/"+in.Slug)
	if replace {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	result := map[string]any{"id": id, "url": "/" + in.Slug}
	if s.cleanupPending {
		result["warning"] = "page saved; temporary storage cleanup pending"
	}
	_ = json.NewEncoder(w).Encode(result)
}

// apiIndex is the private API contract in JSON form: version, endpoints,
// limits and error codes. It mirrors contract.md. Public listener never serves it.
func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, "method not allowed", false)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	index := map[string]any{
		"version": APIVersion,
		"endpoints": []map[string]string{
			{"method": "GET", "path": "/api", "description": "This index: version, endpoints, limits and examples."},
			{"method": "POST", "path": "/api/pages", "description": "Publish a private page. Body: title, agent or key byline, original_request, exactly one of html or markdown, optional slug, template, capabilities, assets, request_summary, request_source."},
			{"method": "PUT", "path": "/api/pages/{slug}", "description": "Replace a page completely. The slug cannot change; omitted assets are removed."},
		},
		"reading": map[string]string{
			"reader":  "GET /{slug} renders the private owner reader with metadata.",
			"content": "GET /_content/{slug} renders report HTML only, without private metadata.",
			"asset":   "GET /_content/{slug}?asset=name.png or GET /_assets/{slug}/name.png serves that page's managed image.",
		},
		"limits": map[string]any{
			"request_bytes":      16 << 20,
			"html_bytes":         2 << 20,
			"markdown_bytes":     2 << 20,
			"asset_bytes_each":   4 << 20,
			"asset_count":        32,
			"default_slug_chars": 3,
			"slug_chars":         s.preferences().LinkLength,
		},
		"error_codes": []string{
			ErrAuthenticationRequired, ErrBrowserOriginForbidden, ErrForbidden,
			ErrUnsupportedMediaType, ErrInvalidJSON, ErrValidationFailed,
			ErrSlugConflict, ErrPublicSlugUnavailable, ErrNotFound,
			ErrMethodNotAllowed, ErrPayloadTooLarge, ErrStorageUnavailable,
			ErrCleanupPending, ErrSharingUnavailable, ErrLinkAllocationFailed,
		},
		"example": map[string]any{
			"request":  map[string]string{"method": "POST", "path": "/api/pages"},
			"response": map[string]string{"id": "...", "url": "/AbC"},
		},
	}
	_ = json.NewEncoder(w).Encode(index)
}

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var assetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}\.(png|jpg|jpeg|gif|webp)$`)

func validSlug(slug string) bool {
	switch strings.ToLower(slug) {
	case "settings", "new", "manage", "import", "api", "healthz", "library", "assets", "admin", "login", "logout":
		return false
	}
	return slugPattern.MatchString(slug)
}
func validatePublication(in publication) error {
	if len(strings.Fields(in.RequestSummary)) > 40 || len(in.RequestSummary) > 1000 {
		return fmt.Errorf("request_summary must be at most 40 words and 1000 bytes")
	}
	if len(in.RequestSource) > 80 || strings.ContainsAny(in.RequestSource, "\r\n") {
		return fmt.Errorf("request_source must be a single label of at most 80 bytes")
	}

	if strings.TrimSpace(in.Title) == "" || len(in.Title) > 300 || strings.TrimSpace(in.Agent) == "" || len(in.Agent) > 100 || strings.TrimSpace(in.OriginalRequest) == "" || len(in.OriginalRequest) > 32768 || strings.TrimSpace(in.HTML) == "" || len(in.HTML) > 2<<20 {
		return fmt.Errorf("title, agent, original_request and html are required and must fit documented limits")
	}
	if in.Slug != "" && !validSlug(in.Slug) {
		return fmt.Errorf("invalid or reserved slug")
	}
	if len(in.Assets) > 32 {
		return fmt.Errorf("too many assets")
	}
	for name, data := range in.Assets {
		if !assetName.MatchString(name) || len(data) == 0 || len(data) > 4<<20 {
			return fmt.Errorf("invalid asset name or size")
		}
		media := http.DetectContentType(data)
		switch media {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			return fmt.Errorf("assets must be PNG, JPEG, GIF or WebP images")
		}
	}
	return nil
}

var safeHost = regexp.MustCompile(`^[A-Za-z0-9.\[\]:-]+$`)

func (s *Server) removeObject(object string) {
	if err := os.RemoveAll(filepath.Join(s.cfg.DataDir, "objects", object)); err != nil {
		s.cleanupPending = true
		log.Print("Cairn: temporary object cleanup failed; will retry before next publication and at restart")
	}
}
