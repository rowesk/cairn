package cairn

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed ui/editor.html
var editorHTML string

//go:embed ui/editor.js
var editorBodyJS string

var editorJS = focusJS + "\n" + editorBodyJS
var editorView = pageView("editor", editorHTML, nil)

type editorData struct {
	ownerChrome
	Theme                                     string
	CSRF                                      formToken
	Title, Markdown, Slug, Images, PublicBase string
	Shared, PublicReady                       bool
	Notice                                    *notice
	Errors                                    fieldErrors
	// Host is the private origin's host, for the address preview.
	Host string
	// Page is set when editing an existing page: its slug, the address
	// the form posts to, and the version a draft was written against.
	Page, Action, DraftBase string
	// SavedTitle is the page's title as saved, for the way back to it.
	SavedTitle string
	// DraftKey keeps a saved page's browser draft by its id, not its
	// address, so a new page that reuses a deleted page's link name never
	// opens with the old draft.
	DraftKey  string
	Protected bool
	// Images already on the page stay on the server; the form sends only
	// newly pasted ones. Kept lists their names for the preview.
	Kept []string
}

// Editing reports whether the editor holds a saved page.
func (d editorData) Editing() bool { return d.Page != "" }

// PublicOrigin is the start of every public link, without a trailing slash.
func (d editorData) PublicOrigin() string { return strings.TrimRight(d.PublicBase, "/") }

// PublicHost is the host of public links, without the scheme.
func (d editorData) PublicHost() string {
	if u, err := url.Parse(d.PublicBase); err == nil {
		return u.Host
	}
	return ""
}

// editTarget is a saved page the editor opens: where its Markdown and
// images live and how it is shared.
type editTarget struct {
	page                 savedPage
	shared, protected    bool
	publicSlug, template string
	markdown             string
	assets               map[string][]byte
}

func (s *Server) newPage(w http.ResponseWriter, r *http.Request) {
	s.editor(w, r, nil)
}

// editPage opens a saved page in the editor and saves it as a replacement
// that keeps its address, date, sharing, password and images. Only pages
// that kept their Markdown can be edited.
func (s *Server) editPage(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/edit/")
	if !validSlug(slug) {
		s.notFound(w, r)
		return
	}
	s.mu.RLock()
	target, err := s.loadEditTarget(slug)
	s.mu.RUnlock()
	if err == sql.ErrNoRows || os.IsNotExist(err) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.showError(w, http.StatusInternalServerError, problemStorage(r.URL.RequestURI()))
		return
	}
	if !editableSource(target.page.FileName) {
		s.mu.RLock()
		reason, next := s.editReason(target.page)
		s.mu.RUnlock()
		// The heading says Cairn can't edit it; the message says why, once.
		if r, size := utf8.DecodeRuneInString(reason); size > 0 {
			reason = string(unicode.ToUpper(r)) + reason[size:]
		}
		s.showError(w, http.StatusConflict, problem{Title: "Cairn can’t edit this page", Message: reason + ". " + next, Action: "Back to the page", Href: "/" + slug})
		return
	}
	s.editor(w, r, target)
}

var templatePattern = regexp.MustCompile(`<main data-template="(report|comparison|visual)"`)

// loadEditTarget reads what the editor needs to reopen a page.
func (s *Server) loadEditTarget(slug string) (*editTarget, error) {
	p, err := s.getPage(slug)
	if err != nil {
		return nil, err
	}
	t := &editTarget{page: p, assets: map[string][]byte{}}
	var publicSlug sql.NullString
	if err := s.db.QueryRow(`SELECT share_enabled,public_slug,coalesce(password_hash,'')!='' FROM pages WHERE id=?`, p.ID).Scan(&t.shared, &publicSlug, &t.protected); err != nil {
		return nil, err
	}
	t.publicSlug = publicSlug.String
	if t.publicSlug == "" {
		t.publicSlug = p.Slug
	}
	if !editableSource(p.FileName) {
		return t, nil
	}
	dir := filepath.Join(s.cfg.DataDir, "objects", p.Object)
	source, err := os.ReadFile(filepath.Join(dir, "source.bin"))
	if err != nil {
		return nil, err
	}
	t.markdown = string(source)
	if page, err := os.ReadFile(filepath.Join(dir, "page.html")); err == nil {
		if m := templatePattern.FindSubmatch(page); m != nil {
			t.template = string(m[1])
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "assets"))
	for _, entry := range entries {
		if !assetName.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "assets", entry.Name()))
		if err != nil {
			return nil, err
		}
		t.assets[entry.Name()] = data
	}
	return t, nil
}

// editor shows and saves the writing form, for a new page (target nil) or
// a saved one.
func (s *Server) editor(w http.ResponseWriter, r *http.Request, target *editTarget) {
	w.Header().Set("Referrer-Policy", "same-origin")
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		s.methodNotAllowed(w, "GET, HEAD, POST")
		return
	}
	d := editorData{ownerChrome: s.chrome(), Theme: s.themes().Admin, CSRF: s.formToken(), PublicReady: s.cfg.PublicBaseURL != "", PublicBase: s.cfg.PublicBaseURL, Errors: fieldErrors{}, Host: r.Host, Action: "/new"}
	back := "/new"
	if target != nil {
		p := target.page
		d.Page, d.Action, back = p.Slug, "/edit/"+p.Slug, "/edit/"+p.Slug
		sum := sha256.Sum256([]byte(p.Object))
		d.DraftBase = hex.EncodeToString(sum[:6])
		d.DraftKey = "page:" + p.ID
		d.Title, d.Markdown, d.Shared, d.Protected = p.Title, target.markdown, target.shared, target.protected
		d.SavedTitle = p.Title
		if target.shared {
			d.Slug = target.publicSlug
		}
		for name := range target.assets {
			d.Kept = append(d.Kept, name)
		}
	}
	if r.Method != http.MethodPost {
		s.renderEditor(w, d, http.StatusOK)
		return
	}
	preview := r.URL.Query().Get("preview") == "1"
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	parseErr := r.ParseForm()
	if !sameOrigin(r) {
		if preview {
			s.previewMessage(w, r, http.StatusForbidden, "This preview didn’t come from the editor. Reload the editor and try again.")
			return
		}
		s.showError(w, http.StatusForbidden, problemForeignForm)
		return
	}
	if parseErr != nil {
		if preview {
			s.previewMessage(w, r, http.StatusRequestEntityTooLarge, "This page is too large to preview. Pages hold up to 2 MB of writing and 12 MB of pasted images.")
			return
		}
		s.showError(w, http.StatusRequestEntityTooLarge, problem{Title: "This page is too large to save", Message: "Pages hold up to 2 MB of writing and 12 MB of pasted images. Go back to shorten it or remove some images.", Action: "Back to the editor", Href: back})
		return
	}
	d.Title = r.FormValue("title")
	d.Markdown = r.FormValue("markdown")
	d.Slug = strings.TrimSpace(r.FormValue("slug"))
	d.Images = r.FormValue("images")
	d.Shared = r.FormValue("access") == "public"
	// Preview changes nothing, so it needs only the origin check: an old
	// token never stops a preview.
	if !preview && !s.freshToken(r) {
		d.Notice = informed("This form had expired, so the page wasn’t saved. Your writing is still here. Save it again.")
		s.renderEditor(w, d, http.StatusForbidden)
		return
	}
	prefs := s.preferences()
	// Pages written here have no original request: the owner wrote them.
	in := publication{Slug: d.Slug, Share: d.Shared, PublicSlug: d.Slug, Title: strings.Join(strings.Fields(d.Title), " "), Markdown: d.Markdown, Agent: prefs.Author, Template: prefs.Template, OwnerWritten: true, Source: &sourceFile{Name: "page.md", Data: []byte(d.Markdown)}}
	if target != nil {
		p := target.page
		in = publication{Slug: p.Slug, Title: in.Title, Markdown: d.Markdown, Agent: p.Agent, Template: target.template, OriginalRequest: p.OriginalRequest, RequestSummary: p.RequestSummary, RequestSource: p.RequestSource, OwnerWritten: true, Source: &sourceFile{Name: p.FileName, Data: []byte(d.Markdown)}}
	}
	imagesProblem := ""
	if d.Images != "" {
		if err := json.Unmarshal([]byte(d.Images), &in.Assets); err != nil {
			imagesProblem = "Cairn couldn’t read the pasted images. Paste them again."
		}
	}
	total := 0
	for _, image := range in.Assets {
		total += len(image)
	}
	if total > 12<<20 {
		imagesProblem = "Pasted images can add up to 12 MB. Remove some, then save again."
	}
	if target != nil {
		// Saved images stay while the writing still refers to them.
		for name, data := range target.assets {
			if _, pasted := in.Assets[name]; !pasted && strings.Contains(d.Markdown, name) {
				if in.Assets == nil {
					in.Assets = map[string][]byte{}
				}
				in.Assets[name] = data
			}
		}
	}
	if preview {
		if imagesProblem != "" {
			s.previewMessage(w, r, http.StatusBadRequest, imagesProblem)
			return
		}
		if in.Title == "" {
			in.Title = "Untitled page"
		}
		if strings.TrimSpace(in.Markdown) == "" {
			in.Markdown = "Start writing to preview your page."
		}
		err := preparePublication(&in)
		if err == nil {
			err = addRuntime(&in)
		}
		if err == nil {
			err = validatePublication(in)
		}
		if err != nil {
			_, message := publishProblem([]byte(`{"code":"` + ErrValidationFailed + `","message":` + strconv.Quote(err.Error()) + `}`))
			s.previewMessage(w, r, http.StatusBadRequest, message)
			return
		}
		for name, data := range in.Assets {
			in.HTML = strings.ReplaceAll(in.HTML, `href="?asset=`+name+`" class="report-image" aria-label="Open image at full size"`, `class="report-image"`)
			in.HTML = strings.ReplaceAll(in.HTML, `src="?asset=`+name+`"`, `src="data:`+http.DetectContentType(data)+`;base64,`+base64.StdEncoding.EncodeToString(data)+`"`)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", reportPolicy(r.Host, "new"))
		w.Write([]byte(reportDocument(reportSchemeFor(s.themes().Admin).apply(in.HTML))))
		return
	}
	status := http.StatusBadRequest
	if imagesProblem != "" {
		d.Notice = failed(imagesProblem)
		s.renderEditor(w, d, status)
		return
	}
	if problem := titleProblem(d.Title); problem != "" {
		d.Errors["title"] = problem
	}
	if strings.TrimSpace(d.Markdown) == "" {
		d.Errors["markdown"] = "Write something before you save the page."
	}
	exceptID := ""
	if target != nil {
		exceptID = target.page.ID
	}
	if target == nil || d.Shared {
		if code, problem := s.linkNameProblem(d.Slug, exceptID); problem != "" {
			d.Errors["slug"], status = problem, code
		}
	}
	if target != nil && d.Shared && !d.PublicReady {
		d.Notice = failed("Public links aren’t set up on this server, so the page stays private. Choose Private under Page access, then save again.")
		d.Errors["access"] = "unavailable"
	}
	if len(d.Errors) == 0 {
		result := httptest.NewRecorder()
		save := r
		if target != nil {
			// A saved page is replaced the way the API replaces one: same
			// address and date, new writing.
			save = r.Clone(r.Context())
			save.Method = http.MethodPut
			save.URL = &url.URL{Path: "/api/pages/" + target.page.Slug}
		}
		s.savePublication(result, save, in)
		if result.Code == http.StatusCreated || (target != nil && result.Code == http.StatusOK) {
			var saved struct{ URL string }
			if json.Unmarshal(result.Body.Bytes(), &saved) == nil {
				if target != nil {
					if notice := s.changeSharing(target, d.Shared, d.Slug); notice != nil {
						d.Notice = notice
						s.renderEditor(w, d, http.StatusInternalServerError)
						return
					}
				}
				// The reader confirms the save when it opens.
				http.Redirect(w, r, savedURL(saved.URL), http.StatusSeeOther)
				return
			}
		}
		status = result.Code
		field, message := publishProblem(result.Body.Bytes())
		if field == "slug" {
			d.Errors["slug"] = message
		} else {
			d.Notice = failed(message)
		}
	}
	if d.Errors["slug"] != "" && d.Notice == nil {
		d.Notice = failed("Change the page address under Page access, then save again.")
	}
	s.renderEditor(w, d, status)
}

// changeSharing applies the access chosen in the editor to a saved page.
// The password stays as it was. It returns a notice when nothing could be
// changed.
func (s *Server) changeSharing(target *editTarget, shared bool, name string) *notice {
	if shared == target.shared && (!shared || name == "" || name == target.publicSlug) {
		return nil
	}
	if name == "" {
		name = target.publicSlug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if shared {
		_, err = s.db.Exec(`UPDATE pages SET share_enabled=1,public_slug=?,share_revision=share_revision+1 WHERE id=?`, name, target.page.ID)
	} else {
		_, err = s.db.Exec(`UPDATE pages SET share_enabled=0,share_revision=share_revision+1 WHERE id=?`, target.page.ID)
	}
	if err != nil {
		return failed("Your writing was saved, but sharing didn’t change. Open Manage page to change it.")
	}
	return nil
}

// previewMessage answers a preview the editor cannot render with one plain
// sentence, shown inside the sandboxed preview frame.
func (s *Server) previewMessage(w http.ResponseWriter, r *http.Request, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", reportPolicy(r.Host, "new"))
	w.WriteHeader(status)
	// A dark theme shows the sentence on dark.
	_, _ = io.WriteString(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="color-scheme" content="`+colorScheme(s.themes().Admin)+`"><title>Preview</title><style>body{margin:0;padding:48px 24px;font:15px/1.6 system-ui,sans-serif;color:GrayText;text-align:center}</style><p>`+template.HTMLEscapeString(message)+`</p>`)
}

func (s *Server) renderEditor(w http.ResponseWriter, d editorData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; "+uiAssetPolicy+"; connect-src 'self'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	editorView.Execute(w, d)
}

// savedURL is a saved page's address with ?saved=1, so the reader that
// opens next says the page was saved.
func savedURL(page string) string {
	u, err := url.Parse(page)
	if err != nil {
		return page
	}
	q := u.Query()
	q.Set("saved", "1")
	u.RawQuery = q.Encode()
	return u.String()
}
