package cairn

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
)

//go:embed ui/editor.html
var editorHTML string

//go:embed ui/editor.js
var editorBodyJS string

var editorJS = focusJS + "\n" + editorBodyJS
var editorHash = func() string {
	sum := sha256.Sum256([]byte(editorJS))
	return base64.StdEncoding.EncodeToString(sum[:])
}()
var editorView = template.Must(template.New("editor").Funcs(uiFunctions).Funcs(template.FuncMap{"editorScript": func() template.JS { return template.JS(editorJS) }}).Parse(editorHTML))

type editorData struct {
	CSRF, Title, Markdown, Error, Slug, Images, PublicBase string
	Shared, PublicReady                                    bool
}

func (s *Server) newPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "same-origin")
	d := editorData{CSRF: s.csrf, PublicReady: s.cfg.PublicBaseURL != "", PublicBase: s.cfg.PublicBaseURL}
	status := http.StatusOK
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "page must fit 2 MiB", 400)
			return
		}
		if !s.ownerRequest(r) {
			http.Error(w, "reload the editor and try again", 403)
			return
		}
		d.Title = r.FormValue("title")
		d.Markdown = r.FormValue("markdown")
		d.Slug = strings.TrimSpace(r.FormValue("slug"))
		d.Images = r.FormValue("images")
		d.Shared = r.FormValue("access") == "public"
		in := publication{Slug: d.Slug, Share: d.Shared, PublicSlug: d.Slug, Title: strings.TrimSpace(d.Title), Markdown: d.Markdown, Agent: s.preferences().Author, Template: s.preferences().Template, OriginalRequest: "Written in Cairn", Source: &sourceFile{Name: "page.md", Data: []byte(d.Markdown)}}
		if d.Images != "" {
			if err := json.Unmarshal([]byte(d.Images), &in.Assets); err != nil {
				d.Error = "Could not read pasted images."
				s.renderEditor(w, d, 400)
				return
			}
		}
		total := 0
		for _, image := range in.Assets {
			total += len(image)
		}
		if total > 12<<20 {
			d.Error = "Pasted images must total no more than 12 MiB."
			s.renderEditor(w, d, 400)
			return
		}
		if r.URL.Query().Get("preview") == "1" {
			if in.Title == "" {
				in.Title = "Untitled page"
			}
			if strings.TrimSpace(in.Markdown) == "" {
				in.Markdown = "Start writing to preview your page."
			}
			if err := preparePublication(&in); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err := addRuntime(&in); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err := validatePublication(in); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			for name, data := range in.Assets {
				in.HTML = strings.ReplaceAll(in.HTML, `href="?asset=`+name+`" class="report-image" aria-label="Open image at full size"`, `class="report-image"`)
				in.HTML = strings.ReplaceAll(in.HTML, `src="?asset=`+name+`"`, `src="data:`+http.DetectContentType(data)+`;base64,`+base64.StdEncoding.EncodeToString(data)+`"`)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", reportPolicy(r.Host, "new"))
			w.Write([]byte(reportLinks(in.HTML)))
			return
		}
		result := httptest.NewRecorder()
		s.savePublication(result, r, in)
		if result.Code == http.StatusCreated {
			var saved struct{ URL string }
			if json.Unmarshal(result.Body.Bytes(), &saved) == nil {
				http.Redirect(w, r, saved.URL, http.StatusSeeOther)
				return
			}
		}
		status = result.Code
		d.Error = apiErrorMessage(result.Body.String())
	}
	s.renderEditor(w, d, status)
}
func (s *Server) renderEditor(w http.ResponseWriter, d editorData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src data:; script-src 'sha256-"+editorHash+"'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	editorView.Execute(w, d)
}
