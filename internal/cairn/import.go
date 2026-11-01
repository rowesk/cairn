package cairn

import (
	"archive/zip"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (s *Server) ownerRequest(r *http.Request) bool {
	return s.ownerToken(r, r.FormValue("csrf"))
}
func (s *Server) ownerToken(r *http.Request, csrf string) bool {
	return sameOrigin(r) && subtle.ConstantTimeCompare([]byte(csrf), []byte(s.csrf)) == 1
}
func sameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Host == r.Host && (origin.Scheme == "http" || origin.Scheme == "https")
}
func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src data:; script-src 'sha256-"+focusScriptHash+"'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		importView.Execute(w, struct{ CSRF, Theme string }{s.csrf, s.themes().Admin})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	// Refuse cross-site posts before writing up to 50 MiB to disk.
	if !sameOrigin(r) {
		http.Error(w, "reload the import form and try again", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	u, err := s.readUpload(r, "document", 4<<20)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			http.Error(w, "the file must fit 50 MiB and each supporting image 4 MiB", 413)
		} else {
			http.Error(w, "invalid import", 400)
		}
		return
	}
	defer u.remove()
	if !s.ownerToken(r, u.fields["csrf"]) {
		http.Error(w, "reload the import form and try again", 403)
		return
	}
	if len(u.files["document"]) != 1 {
		http.Error(w, "choose a file to import", 400)
		return
	}
	doc := u.files["document"][0]
	in := publication{Title: u.fields["title"], Agent: u.fields["agent"], OriginalRequest: u.fields["original_request"], Slug: u.fields["slug"], Template: u.fields["template"], Assets: map[string][]byte{}}
	if strings.TrimSpace(in.Agent) == "" {
		in.Agent = s.preferences().Author
	}
	if strings.TrimSpace(in.OriginalRequest) == "" {
		in.OriginalRequest = "Imported into Cairn"
	}
	images := u.files["assets"]
	asFile := u.fields["as_file"] == "1" || !renderableImport(doc.Name, doc.Size)
	if asFile && len(images) > 0 {
		http.Error(w, "supporting images only apply to pages Cairn can open; remove them to save a download page", 400)
		return
	}
	if !asFile {
		if strings.TrimSpace(in.Title) == "" {
			in.Title = strings.TrimSuffix(doc.Name, filepath.Ext(doc.Name))
		}
		var data []byte
		if data, err = os.ReadFile(doc.Path); err == nil {
			in.Source = &sourceFile{Name: doc.Name, Path: doc.Path, Size: doc.Size}
			err = prepareImport(&in, doc.Name, data)
		}
		if err == nil {
			// Dry run of the save checks, so a file that cannot become a page
			// (non-UTF-8 text, HTML grown past 2 MiB) is kept as a download.
			check := in
			if err = preparePublication(&check); err == nil {
				if err = addRuntime(&check); err == nil {
					err = validatePublication(check)
				}
			}
		}
		if err != nil && len(images) == 0 {
			asFile, err = true, nil
		}
		for _, image := range images {
			if err != nil {
				break
			}
			if _, exists := in.Assets[image.Name]; exists {
				err = fmt.Errorf("duplicate image name")
			}
			in.Assets[image.Name] = image.Data
		}
	}
	if asFile {
		err = filePublication(&in, doc)
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// The same publication path owns all validation and storage semantics.
	result := httptest.NewRecorder()
	s.savePublication(result, r, in)
	if result.Code != http.StatusCreated {
		http.Error(w, result.Body.String(), result.Code)
		return
	}
	var saved struct{ URL string }
	if err := json.Unmarshal(result.Body.Bytes(), &saved); err != nil {
		http.Error(w, "could not read save result", 500)
		return
	}
	http.Redirect(w, r, saved.URL, http.StatusSeeOther)
}

// renderableImport reports whether Cairn can show a file as a page. Anything
// else, including renderable types over their page limits, becomes a file page.
func renderableImport(name string, size int64) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".html", ".htm", ".mmd", ".mermaid", ".txt", ".json", ".php", ".csv", ".xml", ".yaml", ".yml", ".log", ".svg":
		return size <= 2<<20
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return size <= 4<<20
	case ".docx":
		return size <= 10<<20
	}
	return false
}

var importView = template.Must(template.New("import").Parse(`<!doctype html><html lang="en" data-theme="{{.Theme}}"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Import file · Cairn</title><style>` + ownerCSS + `</style><body><main class="form-page"><a href="/">Back to library</a><h1>Import file</h1><p>Bring a page or a file worth keeping.</p><form method="post" enctype="multipart/form-data"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Choose a file<input type="file" name="document" required></label><p class="import-hint">Any file up to 50 MiB. Markdown, HTML, text, JSON, Mermaid and images under 4 MiB open as pages, and DOCX text is extracted. Everything else, such as ZIP, PDF, spreadsheets and Photoshop files, gets a download page. PHP is displayed as source and never executed.</p><label class="check"><input type="checkbox" name="as_file" value="1"> Save as a download page, even if Cairn can open it</label><label>Title<input name="title" maxlength="300" placeholder="Use the filename"></label><details><summary>Page details</summary><label>Prepared by<input name="agent" maxlength="100" placeholder="Use the default author"></label><label>Original request<textarea name="original_request" placeholder="Optional context for later"></textarea><small>This stays private.</small></label><label>Template<select name="template"><option value="report">Report</option><option value="comparison">Comparison</option><option value="visual">Visual</option></select></label><label>Short URL name<input name="slug" placeholder="Leave blank to generate one"></label><label>Supporting images<input type="file" name="assets" accept=".png,.jpg,.jpeg,.gif,.webp" multiple></label></details><button>Save private page</button></form></main>` + focusScriptTag + `</body></html>`))

func prepareImport(in *publication, name string, data []byte) error {
	ext := strings.ToLower(filepath.Ext(name))
	text := func() error {
		if len(data) > 2<<20 || !utf8.Valid(data) {
			return fmt.Errorf("text files must be UTF-8 and fit 2 MiB")
		}
		return nil
	}
	switch ext {
	case ".md", ".markdown":
		if err := text(); err != nil {
			return err
		}
		in.Markdown = string(data)
	case ".html", ".htm":
		if err := text(); err != nil {
			return err
		}
		in.HTML = string(data)
	case ".mmd", ".mermaid":
		if err := text(); err != nil {
			return err
		}
		return importBody(in, `<pre><code class="language-mermaid">`+template.HTMLEscapeString(string(data))+`</code></pre>`)
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		if len(data) > 4<<20 {
			return fmt.Errorf("images must fit 4 MiB")
		}
		asset := "image" + ext
		in.Assets[asset] = data
		in.Template = "visual"
		return importBody(in, `<img alt="`+template.HTMLEscapeString(in.Title)+`" src="?asset=`+asset+`">`)
	case ".txt", ".json", ".php", ".csv", ".xml", ".yaml", ".yml", ".log", ".svg":
		if err := text(); err != nil {
			return err
		}
		if ext == ".json" {
			var formatted bytes.Buffer
			if json.Indent(&formatted, data, "", "  ") == nil {
				data = formatted.Bytes()
			}
		}
		return importBody(in, `<pre><code>`+template.HTMLEscapeString(string(data))+`</code></pre>`)
	case ".docx":
		body, err := docxText(data)
		if err != nil {
			return err
		}
		return importBody(in, body)
	default:
		return fmt.Errorf("this file type is not supported; import Markdown, HTML, text, JSON, Mermaid, an image or a document")
	}
	return nil
}
func importBody(in *publication, body string) error {
	layout := in.Template
	if layout == "" {
		layout = "report"
	}
	var out bytes.Buffer
	if err := reportTemplate.Execute(&out, struct {
		Title, Layout string
		Body          template.HTML
	}{in.Title, layout, template.HTML(body)}); err != nil {
		return err
	}
	in.HTML = out.String()
	return nil
}
func docxText(data []byte) (string, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("could not read DOCX file")
	}
	for _, f := range z.File {
		if f.Name != "word/document.xml" {
			continue
		}
		if f.UncompressedSize64 > 2<<20 {
			return "", fmt.Errorf("DOCX text must fit 2 MiB")
		}
		r, err := f.Open()
		if err != nil {
			return "", err
		}
		defer r.Close()
		raw, err := io.ReadAll(io.LimitReader(r, (2<<20)+1))
		if err != nil || len(raw) > 2<<20 {
			return "", fmt.Errorf("could not read DOCX text")
		}
		d := xml.NewDecoder(bytes.NewReader(raw))
		var out strings.Builder
		inside := false
		for {
			tok, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", fmt.Errorf("invalid DOCX text")
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "p" {
					out.WriteString("<p>")
				}
				if t.Name.Local == "t" {
					inside = true
				}
			case xml.EndElement:
				if t.Name.Local == "p" {
					out.WriteString("</p>")
				}
				if t.Name.Local == "t" {
					inside = false
				}
			case xml.CharData:
				if inside {
					out.WriteString(template.HTMLEscapeString(string(t)))
				}
			}
		}
		return out.String(), nil
	}
	return "", fmt.Errorf("DOCX contains no document text")
}
