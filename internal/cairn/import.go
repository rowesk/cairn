package cairn

import (
	"archive/zip"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (s *Server) ownerRequest(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Host == r.Host && (origin.Scheme == "http" || origin.Scheme == "https") && subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(s.csrf)) == 1
}
func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src data:; script-src 'sha256-"+focusScriptHash+"'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		importView.Execute(w, s.csrf)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "invalid import or too large", 400)
		return
	}
	defer r.MultipartForm.RemoveAll()
	if !s.ownerRequest(r) {
		http.Error(w, "reload the import form and try again", 403)
		return
	}
	file, header, err := r.FormFile("document")
	if err != nil {
		http.Error(w, "choose a file to import", 400)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err != nil || len(data) > 10<<20 {
		http.Error(w, "file must fit 10 MiB", 400)
		return
	}
	in := publication{Title: r.FormValue("title"), Agent: r.FormValue("agent"), OriginalRequest: r.FormValue("original_request"), Slug: r.FormValue("slug"), Template: r.FormValue("template"), Assets: map[string][]byte{}}

	if strings.TrimSpace(in.Title) == "" {
		in.Title = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	}
	if strings.TrimSpace(in.Agent) == "" {
		in.Agent = s.preferences().Author
	}
	if strings.TrimSpace(in.OriginalRequest) == "" {
		in.OriginalRequest = "Imported into Cairn"
	}
	in.Source = &sourceFile{Name: filepath.Base(header.Filename), Data: data}
	if err := prepareImport(&in, header.Filename, data); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	for _, header := range r.MultipartForm.File["assets"] {
		file, err := header.Open()
		if err != nil {
			http.Error(w, "could not read image", 400)
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (4<<20)+1))
		file.Close()
		if readErr != nil {
			http.Error(w, "could not read image", 400)
			return
		}
		if len(data) > 4<<20 {
			http.Error(w, "each image must fit 4 MiB", 400)
			return
		}
		if _, exists := in.Assets[header.Filename]; exists {
			http.Error(w, "duplicate image name", 400)
			return
		}
		in.Assets[header.Filename] = data
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

var importView = template.Must(template.New("import").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Import file · Cairn</title><style>` + ownerCSS + `</style><body><main class="form-page"><a href="/">Back to library</a><h1>Import file</h1><p>Bring something worth keeping.</p><form method="post" enctype="multipart/form-data"><input type="hidden" name="csrf" value="{{.}}"><label>Choose a file<input type="file" name="document" accept=".html,.htm,.md,.markdown,.txt,.json,.mmd,.mermaid,.png,.jpg,.jpeg,.gif,.webp,.pdf,.doc,.docx,.odt,.rtf,.php,.csv,.xml,.yaml,.yml,.log,.svg" required></label><p class="import-hint">Up to 10 MiB. Markdown, HTML, text, JSON, Mermaid and images open as pages. DOCX text is extracted. Other documents are kept for download. PHP is displayed as source and never executed.</p><label>Title<input name="title" maxlength="300" placeholder="Use the filename"></label><details><summary>Page details</summary><label>Prepared by<input name="agent" maxlength="100" placeholder="Use the default author"></label><label>Original request<textarea name="original_request" placeholder="Optional context for later"></textarea><small>This stays private.</small></label><label>Template<select name="template"><option value="report">Report</option><option value="comparison">Comparison</option><option value="visual">Visual</option></select></label><label>Short URL name<input name="slug" placeholder="Leave blank to generate one"></label><label>Supporting images<input type="file" name="assets" accept=".png,.jpg,.jpeg,.gif,.webp" multiple></label></details><button>Save private page</button></form></main>` + focusScriptTag + `</body></html>`))

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
	case ".pdf", ".doc", ".odt", ".rtf":
		return importBody(in, `<h1>`+template.HTMLEscapeString(in.Title)+`</h1><p>This document is saved in your library. Use Download original in the full-page view to open it in your document app.</p>`)
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
