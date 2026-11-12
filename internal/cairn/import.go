package cairn

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// importAccept lists the file types the import form offers. prepareImport
// turns exactly these into pages.
const importAccept = ".html,.htm,.md,.markdown,.txt,.json,.mmd,.mermaid,.png,.jpg,.jpeg,.gif,.webp,.pdf,.doc,.docx,.odt,.rtf,.php,.csv,.xml,.yaml,.yml,.log,.svg"

// importLimit is the largest file the import form takes. The drop zone
// checks the same limit and type list before uploading, with the same words.
const importLimit = 10 << 20

const (
	importUnsupported = "Cairn can’t import this type of file. Choose Markdown, HTML, text, an image, a PDF or a Word document."
	importTooLarge    = "This file is larger than 10 MB. Choose a smaller one."
	importExpired     = "This form had expired, so nothing was imported. Choose the file again and import it."
)

type importData struct {
	ownerChrome
	// LinkRule is the link name rule, for the field's own check.
	LinkRule                              string
	Theme                                 string
	CSRF                                  formToken
	Title, Agent, Request, Template, Slug string
	DefaultAuthor, Host, FileName         string
	Accept, Unsupported, TooLarge         string
	Limit                                 int
	Errors                                fieldErrors
	Notice                                *notice
	OpenDetails                           bool
	// Focus names the first field with an error.
	Focus string
}

func (s *Server) renderImport(w http.ResponseWriter, d importData, status int) {
	formPageHeaders(w)
	w.WriteHeader(status)
	_ = importView.Execute(w, d)
}

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		s.methodNotAllowed(w, "GET, HEAD, POST")
		return
	}
	d := importData{ownerChrome: s.chrome(), LinkRule: linkNameRule, Theme: s.themes().Admin, CSRF: s.formToken(), Template: "report", DefaultAuthor: s.preferences().Author, Host: r.Host, Accept: importAccept, Unsupported: importUnsupported, TooLarge: importTooLarge, Limit: importLimit, Errors: fieldErrors{}}
	if r.Method != http.MethodPost {
		s.renderImport(w, d, http.StatusOK)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		if !sameOrigin(r) {
			s.showError(w, http.StatusForbidden, problemForeignForm)
			return
		}
		status := http.StatusBadRequest
		d.Errors["document"] = "Cairn couldn’t read that upload. Choose the file again."
		if isPayloadTooLarge(err) {
			status = http.StatusRequestEntityTooLarge
			d.Errors["document"] = importTooLarge
		}
		s.renderImport(w, d, status)
		return
	}
	defer r.MultipartForm.RemoveAll()
	if !sameOrigin(r) {
		s.showError(w, http.StatusForbidden, problemForeignForm)
		return
	}
	d.Title, d.Agent, d.Request, d.Slug = r.FormValue("title"), r.FormValue("agent"), r.FormValue("original_request"), strings.TrimSpace(r.FormValue("slug"))
	switch d.Template = r.FormValue("template"); d.Template {
	case "report", "comparison", "visual":
	default:
		d.Template = "report"
	}
	if !s.freshToken(r) {
		d.Notice = informed(importExpired)
		s.renderImport(w, d, http.StatusForbidden)
		return
	}
	status := http.StatusBadRequest
	if strings.TrimSpace(d.Title) != "" {
		if problem := titleProblem(d.Title); problem != "" {
			d.Errors["title"] = problem
		}
	}
	if len(d.Agent) > 100 || !utf8.ValidString(d.Agent) {
		d.Errors["agent"] = "Shorten the name to 100 characters or fewer."
	}
	if len(d.Request) > 32768 || !utf8.ValidString(d.Request) {
		d.Errors["original_request"] = "Shorten the request to about 30,000 characters or fewer."
	}
	if code, problem := s.linkNameProblem(d.Slug, ""); problem != "" {
		d.Errors["slug"], status = problem, code
	}
	in := publication{Title: d.Title, Agent: d.Agent, OriginalRequest: d.Request, Slug: d.Slug, Template: d.Template, Assets: map[string][]byte{}}
	file, header, err := r.FormFile("document")
	if err != nil {
		d.Errors["document"] = "Choose a file to import."
	} else {
		defer file.Close()
		d.FileName = filepath.Base(header.Filename)
		data, err := io.ReadAll(io.LimitReader(file, importLimit+1))
		switch {
		case err != nil:
			d.Errors["document"] = "Cairn couldn’t read that file. Choose it again."
		case len(data) > importLimit:
			d.Errors["document"], status = importTooLarge, http.StatusRequestEntityTooLarge
		default:
			if strings.TrimSpace(in.Title) == "" {
				in.Title = importTitle(d.FileName, data)
			}
			if strings.TrimSpace(in.Agent) == "" {
				in.Agent = d.DefaultAuthor
			}
			// The owner brought this file in; a request is optional.
			in.OwnerWritten = true
			in.Source = &sourceFile{Name: d.FileName, Data: data}
			if err := prepareImport(&in, header.Filename, data); err != nil {
				d.Errors["document"] = err.Error()
			}
		}
	}
	for _, header := range r.MultipartForm.File["assets"] {
		if d.Errors["assets"] != "" {
			break
		}
		file, err := header.Open()
		if err != nil {
			d.Errors["assets"] = "Cairn couldn’t read one of the images. Choose them again."
			break
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (4<<20)+1))
		file.Close()
		switch {
		case readErr != nil:
			d.Errors["assets"] = "Cairn couldn’t read one of the images. Choose them again."
		case len(data) > 4<<20:
			d.Errors["assets"] = "Each image can be up to 4 MB. “" + header.Filename + "” is larger."
		case in.Assets[header.Filename] != nil:
			d.Errors["assets"] = "Two images are called “" + header.Filename + "”. Rename one and choose them again."
		default:
			in.Assets[header.Filename] = data
		}
	}
	if len(d.Errors) == 0 {
		// The same publication path owns all validation and storage semantics.
		result := httptest.NewRecorder()
		s.savePublication(result, r, in)
		if result.Code == http.StatusCreated {
			var saved struct{ URL string }
			if err := json.Unmarshal(result.Body.Bytes(), &saved); err == nil {
				http.Redirect(w, r, saved.URL, http.StatusSeeOther)
				return
			}
		}
		status = result.Code
		field, message := publishProblem(result.Body.Bytes())
		switch field {
		case "slug":
			d.Errors["slug"] = message
		case "images":
			d.Errors["assets"] = message
		default:
			d.Notice = failed(message)
		}
	}
	d.OpenDetails = d.Errors["agent"] != "" || d.Errors["original_request"] != "" || d.Errors["slug"] != "" || d.Errors["assets"] != ""
	// Focus lands on the first field to fix, in the order they appear.
	for _, name := range []string{"document", "title", "agent", "original_request", "slug", "assets"} {
		if d.Errors[name] != "" {
			d.Focus = name
			break
		}
	}
	if d.Errors["document"] == "" {
		// Browsers never keep a chosen file when a form loads again.
		d.Errors["rechoose"] = "Choose the file again to import it."
	}
	s.renderImport(w, d, status)
}

func prepareImport(in *publication, name string, data []byte) error {
	ext := strings.ToLower(filepath.Ext(name))
	text := func() error {
		if len(data) > 2<<20 {
			return fmt.Errorf("Text files can be up to 2 MB. This one is larger.")
		}
		if !utf8.Valid(data) {
			return fmt.Errorf("Cairn couldn’t read this file as text. Save it as UTF-8 and try again.")
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
			return fmt.Errorf("Images can be up to 4 MB. This one is larger.")
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
		return importBody(in, `<h1>`+template.HTMLEscapeString(in.Title)+`</h1>`+fileCard(filepath.Base(name), ext, len(data)))
	default:
		return fmt.Errorf("%s", importUnsupported)
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
		return "", fmt.Errorf("Cairn couldn’t open this Word document. Save it again as .docx and try again.")
	}
	for _, f := range z.File {
		if f.Name != "word/document.xml" {
			continue
		}
		if f.UncompressedSize64 > 2<<20 {
			return "", fmt.Errorf("This Word document holds more than 2 MB of text. Split it and import each part.")
		}
		r, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("Cairn couldn’t read the text in this Word document. Save it again as .docx and try again.")
		}
		defer r.Close()
		raw, err := io.ReadAll(io.LimitReader(r, (2<<20)+1))
		if err != nil || len(raw) > 2<<20 {
			return "", fmt.Errorf("Cairn couldn’t read the text in this Word document. Save it again as .docx and try again.")
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
				return "", fmt.Errorf("Cairn couldn’t read the text in this Word document. Save it again as .docx and try again.")
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
	return "", fmt.Errorf("This Word document has no text to import.")
}

// Titles from inside a file, as cairn publish reads them: a Markdown file's
// first "# " heading, an HTML file's <title>.
var (
	importMarkdownTitle = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t#]*$`)
	importHTMLTitle     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// importTitle is the title of an import whose title field was left blank:
// the file's own title when it has one, else its name without extension.
func importTitle(name string, data []byte) string {
	title := ""
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		if m := importMarkdownTitle.FindSubmatch(data); m != nil {
			title = string(m[1])
		}
	case ".html", ".htm":
		if m := importHTMLTitle.FindSubmatch(data); m != nil {
			title = html.UnescapeString(string(m[1]))
		}
	}
	if title = strings.Join(strings.Fields(title), " "); title == "" || !utf8.ValidString(title) || len(title) > 300 {
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
	return title
}

// documentKinds names the documents Cairn keeps but cannot show as text, in
// the fewest words, so a library row reads "PDF · 47 KB" whole on a phone.
var documentKinds = map[string]string{".pdf": "PDF", ".doc": "Word", ".odt": "OpenDocument", ".rtf": "RTF"}

// legacyDocumentKinds are the longer names file cards carried before, for
// pages imported then.
var legacyDocumentKinds = map[string]string{"PDF document": "PDF", "Word document": "Word", "OpenDocument text": "OpenDocument", "Rich Text document": "RTF"}

// fileCardDownload is the card's Download button. Delivery takes it out of
// the page a visitor sees (fileCardFor).
const fileCardDownload = `<a class="file-card-download" href="?download=1" target="_blank" rel="noopener">Download</a>`

// fileCard is the body of a document Cairn keeps but cannot show: its name,
// kind and size, and a Download button for the original. Browsers will not
// show a PDF inside the reader's sandboxed frame, so the page offers the
// file itself. The card draws in the report's own colours, so it follows
// the theme like any page Cairn rendered.
func fileCard(name, ext string, size int) string {
	kind := documentKinds[ext]
	label := strings.ToUpper(strings.TrimPrefix(ext, "."))
	return `<style>` + fileCardCSS + `</style>` +
		`<div class="file-card"><span class="file-card-icon" aria-hidden="true">` + template.HTMLEscapeString(label) + `</span>` +
		`<p class="file-card-text"><strong>` + template.HTMLEscapeString(name) + `</strong><br>` + kind + ` · ` + strings.Replace(byteSize(size), " ", "\u00a0", 1) + `</p>` +
		fileCardDownload + `</div>`
}

// fileCardFor fits a stored file card to who is reading: older cards take
// the short kind names, and a visitor's copy has no Download, because a
// shared page shares the page, never the file it was made from.
func fileCardFor(content string, owner bool) string {
	if !strings.Contains(content, `class="file-card"`) {
		return content
	}
	for long, short := range legacyDocumentKinds {
		content = strings.Replace(content, "</strong><br>"+long+" · ", "</strong><br>"+short+" · ", 1)
	}
	if !owner {
		content = fileCardButton.ReplaceAllString(content, "")
	}
	return content
}

// fileCardButton finds the card's Download button however the stored page
// orders its attributes.
var fileCardButton = regexp.MustCompile(`<a class="file-card-download"[^>]*>Download</a>`)

const fileCardCSS = `.file-card{display:flex;align-items:center;gap:16px;max-width:43.5rem;margin:32px auto;padding:16px 16px 16px 20px;border:1px solid var(--report-rule);border-radius:12px;background:var(--report-tint);color:var(--report-ink);font:15px/1.5 var(--report-sans)}` +
	`.file-card-icon{display:grid;flex-shrink:0;place-items:center;width:44px;height:52px;border:1px solid var(--report-rule);border-radius:4px 12px 4px 4px;background:var(--report-paper);color:var(--report-muted);font-size:12px;font-weight:600;letter-spacing:.04em}` +
	`.file-card-text{flex:1;min-width:0;margin:0;color:var(--report-muted);font-size:13px;overflow-wrap:anywhere}` +
	`.file-card-text strong{color:var(--report-ink);font-size:15px;font-weight:600}` +
	`.file-card-download{display:inline-flex;flex-shrink:0;align-items:center;min-height:44px;padding:0 18px;border-radius:7px;background:var(--report-accent);color:var(--report-paper);font-weight:500;text-decoration:none}` +
	`.file-card-download:focus-visible{outline:2px solid var(--report-accent);outline-offset:3px}` +
	`@media (max-width:480px){.file-card{flex-wrap:wrap}.file-card-download{width:100%;justify-content:center}}`

// byteSize says a file's size the way the drop zone does: 240 KB, 1.4 MB.
func byteSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d bytes", n)
	case n < 1<<20:
		return fmt.Sprintf("%d KB", (n+512)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}
