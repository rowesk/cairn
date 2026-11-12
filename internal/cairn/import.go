package cairn

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// An empty accept list lets the owner choose any file.
const importAccept = ""

// importLimit is the largest file the import form takes. The drop zone
// checks the same limit and type list before uploading, with the same words.
const importLimit = maxFileBytes

const (
	importUnsupported = "Choose a file to import."
	importTooLarge    = "This file is larger than 50 MiB. Choose a smaller one."
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
	if !sameOrigin(r) {
		s.showError(w, 403, problemForeignForm)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	u, err := s.readUpload(r, "document", 4<<20)
	if err != nil {
		status := 400
		d.Errors["document"] = "Cairn could not read that upload. Choose the file again."
		if errors.Is(err, errUploadTooLarge) {
			status = 413
			d.Errors["document"] = "The file must fit 50 MiB and each supporting image 4 MiB."
		}
		s.renderImport(w, d, status)
		return
	}
	defer u.remove()
	r.Form = map[string][]string{}
	for k, v := range u.fields {
		r.Form[k] = []string{v}
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
	if strings.TrimSpace(in.Agent) == "" {
		in.Agent = d.DefaultAuthor
	}
	in.OwnerWritten = true
	if len(u.files["document"]) != 1 {
		d.Errors["document"] = "Choose a file to import."
	} else {
		doc := u.files["document"][0]
		d.FileName = doc.Name
		images := u.files["assets"]
		asFile := u.fields["as_file"] == "1" || !renderableImport(doc.Name, doc.Size)
		if asFile && len(images) > 0 {
			d.Errors["assets"] = "Supporting images only apply to pages Cairn can open. Remove them to save a file page."
		}
		if !asFile {
			var data []byte
			if data, err = os.ReadFile(doc.Path); err == nil {
				if strings.TrimSpace(in.Title) == "" {
					in.Title = importTitle(doc.Name, data)
				}
				in.Source = &sourceFile{Name: doc.Name, Path: doc.Path, Size: doc.Size}
				err = prepareImport(&in, doc.Name, data)
			}
			if err == nil {
				// Dry run of the save checks, so a file that cannot become a page
				// (non-UTF-8 text, HTML grown past 2 MiB) is kept as a download.
				check := in
				err = validatePublication(check)
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
			d.Errors["document"] = err.Error()
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
