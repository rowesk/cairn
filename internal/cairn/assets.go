package cairn

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// The interface stylesheet and fonts are served from content-hashed URLs
// under /_ui/ on both listeners, so browsers cache them for a year and a new
// build gets new URLs. Nothing under /_ui/ depends on the request or on
// stored data.

//go:embed ui/*.css
var styleFiles embed.FS

//go:embed ui/fonts/*.woff2
var fontFiles embed.FS

// stylesheetOrder is the cascade order of ui/*.css. Later files win ties, so
// themes and focus come last. Add a new surface file before "themes".
var stylesheetOrder = []string{"tokens", "base", "shell", "forms", "library", "editor", "settings", "reader", "feedback", "themes", "focus"}

// uiAsset is one immutable file under /_ui/.
type uiAsset struct {
	body, gzipped []byte
	contentType   string
}

var (
	uiAssets      = map[string]*uiAsset{}
	stylesheetURL string
	fontURL       = map[string]string{}
	// scriptURL maps a page script's name to its published address. Every
	// page script starts with the focus script.
	scriptURL = map[string]string{}
)

// pageScripts are the scripts pages load from /_ui/. Each is one file, so a
// page makes one script request, and the browser keeps it for a year.
func pageScripts() map[string]string {
	return map[string]string{"focus": focusJS, "library": libraryJS, "forms": formsJS, "settings": settingsJS, "editor": editorJS, "reader": readerJS}
}

func init() {
	fonts, err := fs.Glob(fontFiles, "ui/fonts/*.woff2")
	if err != nil || len(fonts) == 0 {
		panic("cairn: no embedded fonts")
	}
	// Every stylesheet must have a place in the cascade.
	sheets, _ := fs.Glob(styleFiles, "ui/*.css")
	if len(sheets) != len(stylesheetOrder) {
		panic(fmt.Sprintf("cairn: ui/ holds %d stylesheets but stylesheetOrder lists %d", len(sheets), len(stylesheetOrder)))
	}
	var css strings.Builder
	for _, name := range stylesheetOrder {
		data, err := styleFiles.ReadFile("ui/" + name + ".css")
		if err != nil {
			panic("cairn: missing stylesheet " + name)
		}
		fmt.Fprintf(&css, "/* %s.css */\n", name)
		css.Write(data)
		css.WriteString("\n")
	}
	stylesheet := css.String()
	for _, file := range fonts {
		data, _ := fontFiles.ReadFile(file)
		base := path.Base(file)
		published := hashedName(base, data)
		uiAssets[published] = &uiAsset{body: data, contentType: "font/woff2"}
		fontURL[strings.TrimSuffix(strings.TrimPrefix(base, "dm-sans-"), ".woff2")] = "/_ui/" + published
		// tokens.css names fonts by their source file; publish the hashed name.
		stylesheet = strings.ReplaceAll(stylesheet, "url("+base+")", "url("+published+")")
	}
	published := hashedName("cairn.css", []byte(stylesheet))
	uiAssets[published] = &uiAsset{body: []byte(stylesheet), gzipped: gzipBytes([]byte(stylesheet), gzip.BestCompression), contentType: "text/css; charset=utf-8"}
	stylesheetURL = "/_ui/" + published
	for name, js := range pageScripts() {
		published := hashedName(name+".js", []byte(js))
		uiAssets[published] = &uiAsset{body: []byte(js), gzipped: gzipBytes([]byte(js), gzip.BestCompression), contentType: "text/javascript; charset=utf-8"}
		scriptURL[name] = "/_ui/" + published
	}
}

// scriptFor is the template function that names a page script's address.
func scriptFor(name string) string {
	url, ok := scriptURL[name]
	if !ok {
		panic("cairn: no page script named " + name)
	}
	return url
}

// hashedName turns name.ext into name.<first 12 hex of sha256>.ext.
func hashedName(name string, data []byte) string {
	sum := sha256.Sum256(data)
	ext := path.Ext(name)
	return strings.TrimSuffix(name, ext) + "." + hex.EncodeToString(sum[:6]) + ext
}

func gzipBytes(data []byte, level int) []byte {
	var out bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&out, level)
	zw.Write(data)
	zw.Close()
	return out.Bytes()
}

// fontPreloads lists the font files every page draws its first text with.
// Minimal uses Arial and loads no web font at all.
func fontPreloads(theme string) []string {
	if theme == "minimal" {
		return nil
	}
	return []string{fontURL["400"], fontURL["500"]}
}

// serveUI answers /_ui/ on both listeners. Only published names exist.
func serveUI(w http.ResponseWriter, r *http.Request, missing http.HandlerFunc) {
	asset := uiAssets[strings.TrimPrefix(r.URL.Path, "/_ui/")]
	if asset == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		missing(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", asset.contentType)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	body := asset.body
	if asset.gzipped != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("Content-Encoding", "gzip")
			body = asset.gzipped
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

// acceptsGzip reports whether the client takes gzip, honouring q=0.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if name = strings.TrimSpace(strings.ToLower(name)); name != "gzip" && name != "*" {
			continue
		}
		params = strings.ReplaceAll(strings.ToLower(params), " ", "")
		if q, ok := strings.CutPrefix(params, "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// compressible names the text responses worth compressing on the fly.
func compressible(contentType string) bool {
	media, _, _ := strings.Cut(contentType, ";")
	switch strings.TrimSpace(strings.ToLower(media)) {
	case "text/html", "text/css", "text/javascript", "application/javascript", "application/json":
		return true
	}
	return false
}

var gzipWriters = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
	return zw
}}

// compressResponses gzips HTML, CSS, JavaScript and JSON responses for
// clients that accept it. Handlers that set Content-Encoding themselves,
// partial or bodiless responses, and range-capable file responses pass
// through unchanged.
func compressResponses(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	cw := &compressWriter{ResponseWriter: w, accepts: acceptsGzip(r) && r.Method != http.MethodHead}
	defer cw.close()
	next(cw, r)
}

type compressWriter struct {
	http.ResponseWriter
	accepts, decided bool
	zw               *gzip.Writer
}

func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *compressWriter) WriteHeader(status int) {
	if !c.decided {
		c.decided = true
		h := c.Header()
		if status >= 200 && status != http.StatusNoContent && status != http.StatusPartialContent && status != http.StatusNotModified &&
			h.Get("Content-Encoding") == "" && h.Get("Accept-Ranges") == "" && compressible(h.Get("Content-Type")) {
			h.Add("Vary", "Accept-Encoding")
			if c.accepts {
				h.Del("Content-Length")
				h.Set("Content-Encoding", "gzip")
				if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
					h.Set("ETag", "W/"+etag)
				}
				c.zw = gzipWriters.Get().(*gzip.Writer)
				c.zw.Reset(c.ResponseWriter)
			}
		}
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *compressWriter) Write(data []byte) (int, error) {
	if !c.decided {
		c.WriteHeader(http.StatusOK)
	}
	if c.zw != nil {
		return c.zw.Write(data)
	}
	return c.ResponseWriter.Write(data)
}

func (c *compressWriter) close() {
	if c.zw != nil {
		c.zw.Close()
		gzipWriters.Put(c.zw)
		c.zw = nil
	}
}
