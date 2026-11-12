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
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

//go:embed runtime/*
var runtimeFiles embed.FS

// runtimeScripts are the packaged libraries a report may load. Stored pages
// name them by their plain /_runtime/<name> path; reports are delivered with
// a ?v=<hash> URL that browsers may cache for a year.
var runtimeScripts = func() map[string]*runtimeScript {
	scripts := map[string]*runtimeScript{}
	for _, name := range []string{"mermaid.js", "echarts.js", "boot.js"} {
		data, err := fs.ReadFile(runtimeFiles, "runtime/"+name)
		if err != nil {
			panic("cairn: missing runtime " + name)
		}
		sum := sha256.Sum256(data)
		scripts[name] = &runtimeScript{body: data, version: hex.EncodeToString(sum[:6]), etag: fmt.Sprintf(`"%x"`, sum)}
	}
	return scripts
}()

type runtimeScript struct {
	body, gzipped []byte
	version, etag string
}

// compressRuntime gzips the runtime scripts once per process. Open starts it
// in the background so the first report does not wait for 6.6 MB of
// compression.
var compressRuntime = sync.OnceFunc(func() {
	for _, script := range runtimeScripts {
		script.gzipped = gzipBytes(script.body, gzip.BestCompression)
	}
})

// serveRuntimeFile serves one of the named runtime scripts. Anything else
// under /_runtime/, including the folder itself and the licence files kept
// beside the scripts, gets the listener's own not-found page: no listing.
func serveRuntimeFile(w http.ResponseWriter, r *http.Request, missing http.HandlerFunc) {
	script := runtimeScripts[strings.TrimPrefix(r.URL.Path, "/_runtime/")]
	if script == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		missing(w, r)
		return
	}
	// Report frames have an opaque origin, so they load these cross-origin.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	serveRuntime(w, r, script)
}

func serveRuntime(w http.ResponseWriter, r *http.Request, script *runtimeScript) {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Add("Vary", "Accept-Encoding")
	if r.URL.Query().Get("v") == script.version {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
	body, etag := script.body, script.etag
	if acceptsGzip(r) {
		compressRuntime()
		h.Set("Content-Encoding", "gzip")
		body, etag = script.gzipped, strings.TrimSuffix(script.etag, `"`)+`-gzip"`
	}
	h.Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

// versionRuntime points a report's runtime script tags at the current
// versioned URLs. When the report has no scripts of its own, the runtime
// loads with defer so the report's text paints before the libraries arrive;
// boot.js still runs after Mermaid and ECharts because deferred scripts keep
// their order. Reports with their own inline scripts keep blocking tags,
// since those scripts may call the libraries while the page parses.
func versionRuntime(doc string) string {
	if !strings.Contains(doc, `<script src="/_runtime/`) {
		return doc
	}
	attr := ""
	if !hasOwnScripts(doc) {
		attr = " defer"
		// Mermaid is by far the heaviest library. With no script of the
		// report's own waiting for it, boot.js fetches it only when a diagram
		// comes near the screen (or the page is printed).
		const mermaid, boot = `<script src="/_runtime/mermaid.js"></script>`, `<script src="/_runtime/boot.js"></script>`
		if strings.Contains(doc, mermaid) && strings.Contains(doc, boot) {
			doc = strings.ReplaceAll(doc, mermaid, "")
			doc = strings.ReplaceAll(doc, boot, `<script src="/_runtime/boot.js?v=`+runtimeScripts["boot.js"].version+`" defer data-mermaid="/_runtime/mermaid.js?v=`+runtimeScripts["mermaid.js"].version+`"></script>`)
		}
	}
	for _, name := range []string{"mermaid.js", "echarts.js", "boot.js"} {
		doc = strings.ReplaceAll(doc, `<script src="/_runtime/`+name+`"></script>`, `<script src="/_runtime/`+name+`?v=`+runtimeScripts[name].version+`"`+attr+`></script>`)
	}
	return doc
}

// hasOwnScripts reports whether a report has any script beyond Cairn's
// runtime tags.
func hasOwnScripts(doc string) bool {
	z := html.NewTokenizer(strings.NewReader(doc))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			if string(name) != "script" {
				continue
			}
			src := ""
			for more {
				var key, value []byte
				key, value, more = z.TagAttr()
				if string(key) == "src" {
					src = string(value)
				}
			}
			if _, ok := runtimeScripts[strings.TrimPrefix(src, "/_runtime/")]; !ok || !strings.HasPrefix(src, "/_runtime/") {
				return true
			}
		}
	}
}

func addRuntime(in *publication) error {
	names := map[string]bool{}
	for _, capability := range in.Capabilities {
		if capability != "mermaid" && capability != "echarts" {
			return fmt.Errorf("unknown capability")
		}
		names[capability] = true
	}
	for _, name := range []string{"mermaid", "echarts"} {
		if strings.Contains(in.HTML, `class="language-`+name+`"`) {
			names[name] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(in.HTML))
	if err != nil {
		return err
	}
	var head *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "head" {
			head = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	first := head.FirstChild
	for _, name := range []string{"mermaid", "echarts", "boot"} {
		if name != "boot" && !names[name] {
			continue
		}
		node := &html.Node{Type: html.ElementNode, Data: "script", Attr: []html.Attribute{{Key: "src", Val: "/_runtime/" + name + ".js"}}}
		head.InsertBefore(node, first)
	}
	var output bytes.Buffer
	if err := html.Render(&output, doc); err != nil {
		return err
	}
	in.HTML = output.String()
	return nil
}
