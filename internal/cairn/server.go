// Package cairn serves a private library of saved reports.
package cairn

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Config struct {
	DataDir, PublisherToken, PublicBaseURL, Owner string
	// PrivateBaseURL is the origin people and agents use to reach the
	// private library, such as https://cairn.example.ts.net. The API builds
	// full links from it, never from a request's Host header. Optional.
	PrivateBaseURL string
	// AllowedHosts are extra names the private listener answers to, beyond
	// IP addresses, localhost, single-label names, names under .ts.net and
	// the private base URL's host. Any other Host header is refused, so a
	// website cannot reach the library by pointing its own name at it.
	AllowedHosts []string
	// Version is reported by the private /healthz.
	Version string
	// Logger receives one line per publication, refused credential and
	// server error. Nil discards them.
	Logger *slog.Logger
	Mail   MailConfig
}

// APIVersion is served by the private GET /api index.
const APIVersion = "1.1"

type Server struct {
	shareKey    []byte
	clickWrites sync.WaitGroup
	// unlocks slows password guessing on shared pages (password.go).
	unlocks *unlockLimiter
	// flashes name pages the owner just deleted (library.go).
	flashes        *deletedFlashes
	csrf           []byte
	cleanupPending bool
	lock           *os.File
	cfg            Config
	db             *sql.DB
	mu             sync.RWMutex
	// The background rebuild of excerpts and the search index (search.go).
	indexStop, indexDone chan struct{}
	indexing             atomic.Bool
	// Idempotency keys whose first request is still running (api.go), and
	// the hourly purge of expired replies.
	idemMu               sync.Mutex
	idemHeld             map[string]bool
	purgeStop, purgeDone chan struct{}
	log                  *slog.Logger
	// The Host names each listener answers (hosts.go).
	privateHosts, publicHosts *hostPolicy
}
type publication struct {
	Share     bool   `json:"-"`
	TargetURL string `json:"-"`
	// ImportedHTML is set only by the owner import path, never from JSON.
	ImportedHTML bool `json:"-"`
	// OwnerWritten marks a page the owner wrote or imported in Cairn. It may
	// have no original request; the API always needs one.
	OwnerWritten    bool              `json:"-"`
	PublicSlug      string            `json:"-"`
	Source          *sourceFile       `json:"-"`
	File            bool              `json:"-"`
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
	Kind, FileName                                             string
	TargetURL                                                  string
	Clicks                                                     int64
	File                                                       bool
	FileSize                                                   int64
}

func Open(cfg Config) (*Server, error) {
	cfg.Owner = strings.TrimSpace(cfg.Owner)
	if cfg.Owner == "" {
		cfg.Owner = "Owner"
	}
	if len(cfg.Owner) > 100 || !utf8.ValidString(cfg.Owner) {
		return nil, fmt.Errorf("owner name must fit 100 UTF-8 bytes")
	}

	if err := cfg.Mail.validate(); err != nil {
		return nil, err
	}
	if cfg.PublicBaseURL != "" && !validOrigin(cfg.PublicBaseURL) {
		return nil, fmt.Errorf("public base URL must be an HTTP(S) origin")
	}
	if cfg.PrivateBaseURL != "" && !validOrigin(cfg.PrivateBaseURL) {
		return nil, fmt.Errorf("private base URL must be an HTTP(S) origin")
	}
	if err := checkAllowedHosts(cfg.AllowedHosts); err != nil {
		return nil, err
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if len(cfg.PublisherToken) < 32 {
		return nil, fmt.Errorf("installation token must have at least 32 characters")
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

 CREATE TABLE IF NOT EXISTS theme_preferences (id INTEGER PRIMARY KEY CHECK(id=1),admin TEXT NOT NULL DEFAULT 'system',reader TEXT NOT NULL DEFAULT '',public TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1);
 INSERT OR IGNORE INTO theme_preferences(id) VALUES(1);
 CREATE TABLE IF NOT EXISTS kindle_preferences (id INTEGER PRIMARY KEY CHECK(id=1),address TEXT NOT NULL DEFAULT '');
 INSERT OR IGNORE INTO kindle_preferences(id) VALUES(1);
 CREATE TABLE IF NOT EXISTS agent_keys (name TEXT PRIMARY KEY, digest BLOB UNIQUE, created_at TEXT NOT NULL, last_used TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS author_profiles (name TEXT PRIMARY KEY,photo BLOB NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO preferences(id,author,template,link_length) VALUES(1,?,'report',3)`, cfg.Owner); err != nil {
		db.Close()
		return nil, err
	}
	if err := migratePages(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateLinkPreferences(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateShortcutPreference(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateThemes(db); err != nil {
		db.Close()
		return nil, err
	}
	for column, definition := range map[string]string{"file_page": "INTEGER NOT NULL DEFAULT 0", "file_name": "TEXT NOT NULL DEFAULT ''", "file_size": "INTEGER NOT NULL DEFAULT 0"} {
		if _, err := db.Exec("SELECT " + column + " FROM pages LIMIT 0"); err != nil {
			if _, err := db.Exec("ALTER TABLE pages ADD COLUMN " + column + " " + definition); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	// Interrupted uploads leave only temporary files here; the lock is held.
	if err := os.RemoveAll(filepath.Join(cfg.DataDir, "uploads")); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(cfg.DataDir, "uploads"), 0700); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(filepath.Join(cfg.DataDir, "cairn.sqlite"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	server := &Server{cfg: cfg, db: db, lock: lock, unlocks: newUnlockLimiter(), flashes: &deletedFlashes{items: map[string]deletedFlash{}}, idemHeld: map[string]bool{}, log: cfg.Logger}
	server.privateHosts = newHostPolicy(true, []string{cfg.PrivateBaseURL}, cfg.AllowedHosts)
	server.publicHosts = newHostPolicy(false, []string{cfg.PublicBaseURL}, nil)
	if err := server.loadShareKey(); err != nil {
		db.Close()
		return nil, err
	}
	if err := server.loadFormKey(); err != nil {
		db.Close()
		return nil, err
	}
	if err := server.removeUnreferencedObjects(); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateLibrary(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateKinds(db, cfg.DataDir); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateAPI(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := server.purgeReplies(); err != nil {
		db.Close()
		return nil, err
	}
	opened = true
	server.startIndexing()
	server.startPurging()
	go compressRuntime()
	return server, nil
}
func (s *Server) Close() error {
	s.stopIndexing()
	s.stopPurging()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clickWrites.Wait()
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

// validOrigin reports whether u is an http or https origin with no path,
// query, fragment or user name.
func validOrigin(u string) bool {
	parsed, err := url.Parse(u)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" && (parsed.Path == "" || parsed.Path == "/")
}

// ServeHTTP serves the private listener, compressing text responses and
// logging publications, refused credentials and server errors.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.logRequests(w, r, "private", func(w http.ResponseWriter, r *http.Request) { compressResponses(w, r, s.serve) })
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if !s.privateHosts.allows(r.Host) {
		s.refuseHost(w, r, false)
		return
	}
	r = withHostPolicy(r, s.privateHosts)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/_runtime/") {
		serveRuntimeFile(w, r, s.notFound)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_ui/") {
		serveUI(w, r, s.notFound)
		return
	}
	if r.URL.Path == "/healthz" {
		s.health(w, r, false)
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
	if strings.HasPrefix(r.URL.Path, "/edit/") {
		s.editPage(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_library/") {
		s.libraryAction(w, r)
		return
	}
	if r.URL.Path == "/new-link" {
		s.newShortLink(w, r)
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
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		s.api(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.methodNotAllowed(w, "GET, HEAD")
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
	if r.URL.Path == "/" {
		s.library(w, r)
		return
	}

	if strings.Contains(r.URL.EscapedPath(), "%") {
		s.notFound(w, r)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/")
	if strings.HasPrefix(slug, "_assets/") {
		parts := strings.Split(slug, "/")
		if len(parts) != 3 || !validSlug(parts[1]) || !assetName.MatchString(parts[2]) {
			s.notFound(w, r)
			return
		}
		p, err := s.getPage(parts[1])
		if err != nil {
			s.notFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		serveObjectFile(w, r, filepath.Join(s.cfg.DataDir, "objects", p.Object, "assets", parts[2]), s.notFound)
		return
	}
	content := strings.HasPrefix(slug, "_content/")
	if content {
		slug = strings.TrimPrefix(slug, "_content/")
	}
	ext := ""
	if !content {
		slug, ext = directParts(slug)
	}
	if !validSlug(slug) {
		s.notFound(w, r)
		return
	}
	if ext != "" {
		var resolved string
		if err := s.db.QueryRow(`SELECT slug FROM pages WHERE slug=? OR public_slug=? ORDER BY slug=? DESC LIMIT 1`, slug, slug, slug).Scan(&resolved); err == nil {
			slug = resolved
		}
	}
	p, err := s.getPage(slug)
	if err != nil || (ext != "" && !strings.EqualFold(ext, directExtension(p))) {
		s.notFound(w, r)
		return
	}
	if p.Kind == "link" {
		if content || ext != "" {
			s.notFound(w, r)
			return
		}
		unlock()
		r.URL.Path = "/manage/" + p.Slug
		s.manage(w, r)
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
	if content && r.URL.Query().Has("asset") {
		name := r.URL.Query().Get("asset")
		if !assetName.MatchString(name) {
			s.notFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		serveObjectFile(w, r, filepath.Join(s.cfg.DataDir, "objects", p.Object, "assets", name), s.notFound)
		return
	}
	if content {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", reportPolicy(r.Host, p.Slug))
		data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "page.html"))
		if err != nil {
			s.notFound(w, r)
			return
		}
		if opensPage(r, true) {
			s.markOpened(p.ID)
		}
		_, _ = w.Write([]byte(reportDocument(reportSchemeOf(r).apply(string(data)))))
		return
	}
	if opensPage(r, false) {
		s.markOpened(p.ID)
	}
	s.ownerReader(w, r, p)
}
func (s *Server) getPage(slug string) (savedPage, error) {
	var p savedPage
	err := s.db.QueryRow(`SELECT id,slug,title,agent,original_request,created_at,object,request_summary,request_source,file_page,file_size,kind,coalesce(target_url,''),clicks FROM pages WHERE slug=?`, slug).Scan(&p.ID, &p.Slug, &p.Title, &p.Agent, &p.OriginalRequest, &p.CreatedAt, &p.Object, &p.RequestSummary, &p.RequestSource, &p.File, &p.FileSize, &p.Kind, &p.TargetURL, &p.Clicks)
	if err == nil {
		p.FileName = s.sourceName(p)
	}
	return p, err
}

// savePublication checks, stores and answers one publication. The API, the
// editor and import all save through it, so they share every rule.
func (s *Server) savePublication(w http.ResponseWriter, r *http.Request, in publication) {
	ctx := publishContextOf(r)
	if ctx == nil {
		ctx = &publishContext{}
	}
	problems, rendered := preparePage(&in)
	if len(problems) > 0 {
		writeAPIProblems(w, http.StatusBadRequest, ErrValidationFailed, problems)
		return
	}
	warnings := append(ctx.warnings, renderWarnings(rendered)...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanupPending {
		if err := s.removeUnreferencedObjects(); err != nil {
			failRequest(r, err)
			writeAPIError(w, http.StatusServiceUnavailable, ErrCleanupPending, "Cairn could not clear old files from its storage. The owner needs to check the data directory's permissions; try again after that.", true)
			return
		}
		s.cleanupPending = false
	}
	replace := r.Method == http.MethodPut
	var previous savedPage
	var err error
	if replace {
		slug := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/pages/"), "/api/files/")
		if !validSlug(slug) {
			noPage(w, slug, true)
			return
		}
		previous, err = s.getPage(slug)
		if err != nil && err != sql.ErrNoRows {
			failRequest(r, err)
			writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "Cairn's storage did not answer. Try again in a moment.", true)
			return
		}
		// Another author's page answers as if it were missing.
		if err == sql.ErrNoRows || !ctx.cred.reaches(previous.Agent) {
			noPage(w, slug, true)
			return
		}
		if previous.Kind == "link" {
			writeAPIError(w, 400, ErrValidationFailed, "Edit short links from Manage.", false)
			return
		}
		if in.Slug != "" && in.Slug != slug {
			writeAPIProblems(w, http.StatusBadRequest, ErrValidationFailed, []FieldError{{Code: FieldInvalidValue, Field: "slug", Message: "slug cannot change when replacing a page. Leave it out, or send " + strconv.Quote(slug) + ".", Actual: clip(in.Slug, 70)}})
			return
		}
		in.Slug = slug
	} else {
		if in.Slug == "" {
			in.Slug, err = s.randomSlug()
			if err != nil {
				failRequest(r, err)
				writeAPIError(w, http.StatusInternalServerError, ErrLinkAllocationFailed, "Cairn could not choose a free address. Try again.", true)
				return
			}
		}
		existing, lookupErr := s.slugHolder(in.Slug)
		if lookupErr != nil {
			failRequest(r, lookupErr)
			writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "Cairn's storage did not answer. Try again in a moment.", true)
			return
		}
		if existing != "" {
			// The same words whether or not the case matches, and never the
			// other page's slug: it may be another author's.
			message := "slug " + strconv.Quote(in.Slug) + " is taken. Slugs that differ only in letter case count as the same. Leave slug out and Cairn chooses a free one; replace a page only when the owner asks."
			writeAPIErrorBody(w, http.StatusConflict, APIError{Code: ErrSlugConflict, Message: message, Field: "slug", Errors: []FieldError{{Code: FieldSlugTaken, Field: "slug", Message: message, Actual: in.Slug}}})
			return
		}
	}
	// Files and links the owner creates are shared at once. Agent
	// uploads stay private until the owner asks, as pages do.
	autoShare := (in.File || in.TargetURL != "") && in.OwnerWritten && !replace
	if autoShare {
		in.Share = true
		for in.TargetURL == "" {
			in.PublicSlug, err = s.randomSlug()
			if err != nil || in.PublicSlug != in.Slug {
				break
			}
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrLinkAllocationFailed, "Cairn could not choose a free address. Try again.", true)
			return
		}
	}
	if in.Share {
		if replace || (s.cfg.PublicBaseURL == "" && !autoShare) {
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
			writeAPIErrorField(w, http.StatusConflict, ErrPublicSlugUnavailable, "public_slug", "public link name is unavailable", false)
			return
		}
	}
	id := previous.ID
	created := previous.CreatedAt
	now := time.Now().UTC().Format(time.RFC3339)
	if !replace {
		id = randomID()
		created = now
	}
	if replace {
		if in.RequestSummary == "" {
			in.RequestSummary = previous.RequestSummary
		}
		if in.RequestSource == "" {
			in.RequestSource = previous.RequestSource
		}
	}
	kind := "page"
	if in.TargetURL != "" {
		kind = "link"
		in.Title = in.Slug
	}
	if in.File {
		kind = "file"
	}
	preview, body := pageText(in.HTML, in.Title)
	var fileName string
	var fileSize int64
	if in.File && in.Source != nil {
		fileName, fileSize = in.Source.Name, in.Source.Size
	}
	sourceName := ""
	if in.Source != nil {
		sourceName = in.Source.Name
	}
	object, err := s.writeObject(in)
	if err != nil {
		failRequest(r, err)
		writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "Cairn could not write the page to its storage. Try again in a moment.", true)
		return
	}
	// The page row and its search index change together.
	stamp := indexStamp(object, in.Title, in.RequestSummary, in.OriginalRequest, in.Agent)
	tx, err := s.db.Begin()
	if err == nil {
		if replace {
			_, err = tx.Exec(`UPDATE pages SET title=?,agent=?,original_request=?,object=?,preview=?,request_summary=?,request_source=?,source_name=?,index_version=?,index_source=?,updated_at=?,file_page=?,file_name=?,file_size=?,kind=? WHERE id=?`, in.Title, in.Agent, in.OriginalRequest, object, preview, in.RequestSummary, in.RequestSource, sourceName, derivedVersion, stamp, now, in.File, fileName, fileSize, kind, id)
		} else {
			// The owner has seen what they wrote or imported themselves.
			opened := ""
			if in.OwnerWritten {
				opened = created
			}
			_, err = tx.Exec(`INSERT INTO pages (id,slug,title,agent,original_request,created_at,object,preview,request_summary,request_source,share_enabled,public_slug,source_name,index_version,index_source,opened_at,updated_at,file_page,file_name,file_size,kind,target_url) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, in.Slug, in.Title, in.Agent, in.OriginalRequest, created, object, preview, in.RequestSummary, in.RequestSource, in.Share, func() any {
				if in.Share {
					return in.PublicSlug
				}
				return nil
			}(), sourceName, derivedVersion, stamp, opened, now, in.File, fileName, fileSize, kind, in.TargetURL)
		}
		if err == nil {
			err = writeIndex(tx, pageIndex{ID: id, Title: in.Title, Summary: in.RequestSummary, Request: in.OriginalRequest, Agent: in.Agent, Body: body})
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	if err != nil {
		s.removeObject(object)
		failRequest(r, err)
		writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "Cairn could not save the page. Try again in a moment.", true)
		return
	}
	if replace {
		s.removeObject(previous.Object)
	}
	result := apiPageWrite{}
	if page, _, err := s.pageResource(in.Slug); err == nil {
		result.apiPage = page
	} else {
		result.apiPage = apiPage{ID: id, Slug: in.Slug, URL: "/" + in.Slug, PrivateURL: s.privateURL("/" + in.Slug), Title: in.Title, Agent: in.Agent, Assets: []string{}, CreatedAt: created, UpdatedAt: now}
	}
	if s.cleanupPending {
		result.Warning = "page saved; temporary storage cleanup pending"
		warnings = append(warnings, apiWarning{Code: "storage_cleanup_pending", Message: "The page was saved, but Cairn could not remove the old version's files. It tries again before the next save."})
	}
	if replace || !in.Share {
		result.PublicURL = nil
	}
	result.Warnings = s.writeWarnings(warnings)
	event, credentialKind := "publish", "owner"
	if replace {
		event = "replace"
	}
	if ctx.cred.Scope != "" {
		credentialKind = ctx.cred.kind()
	}
	noteRequest(r, event, slog.String("slug", in.Slug), slog.String("agent", in.Agent), slog.String("credential", credentialKind), slog.String("format", result.Format), slog.Int("warnings", len(result.Warnings)))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/"+in.Slug)
	if replace {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	_ = apiEncoder(w).Encode(result)
}

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var assetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}\.(png|jpg|jpeg|gif|webp)$`)

func validSlug(slug string) bool {
	return slugPattern.MatchString(slug) && !slices.Contains(reservedSlugs, strings.ToLower(slug))
}

// validatePublication checks a publication's fields and returns the first
// problem. savePublication reports all of them; the editor's preview needs
// one sentence.
func validatePublication(in publication) error {
	if problems := checkFields(in); len(problems) > 0 {
		return &fieldProblem{problems[0]}
	}
	return nil
}

func (s *Server) removeObject(object string) {
	if err := os.RemoveAll(filepath.Join(s.cfg.DataDir, "objects", object)); err != nil {
		s.cleanupPending = true
		log.Print("Cairn: temporary object cleanup failed; will retry before next publication and at restart")
	}
}

// migrateKinds runs once. Read source metadata because an older library may
// not have rebuilt its derived source_name column yet.
func migrateKinds(db *sql.DB, dataDir string) error {
	if _, err := db.Exec("SELECT kind FROM pages LIMIT 0"); err == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("ALTER TABLE pages ADD COLUMN kind TEXT NOT NULL DEFAULT 'page' CHECK(kind IN ('page','file','link'))"); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id,object,file_page FROM pages`)
	if err != nil {
		return err
	}
	type fileRow struct {
		id, name string
		size     int64
	}
	var files []fileRow
	for rows.Next() {
		var id, object string
		var file bool
		if err := rows.Scan(&id, &object, &file); err != nil {
			rows.Close()
			return err
		}
		var source struct{ Name string }
		metadata, _ := os.ReadFile(filepath.Join(dataDir, "objects", object, "source.json"))
		_ = json.Unmarshal(metadata, &source)
		switch strings.ToLower(filepath.Ext(source.Name)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			file = true
		}
		if file {
			var size int64
			if info, err := os.Stat(filepath.Join(dataDir, "objects", object, "source.bin")); err == nil {
				size = info.Size()
			}
			files = append(files, fileRow{id, source.Name, size})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, f := range files {
		if _, err := tx.Exec(`UPDATE pages SET kind='file',file_name=?,file_size=? WHERE id=?`, f.name, f.size, f.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
