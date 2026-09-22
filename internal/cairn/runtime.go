package cairn

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

//go:embed runtime/*
var runtimeFiles embed.FS
var runtimeHandler = func() http.Handler {
	root, _ := fs.Sub(runtimeFiles, "runtime")
	files := http.StripPrefix("/_runtime/", http.FileServer(http.FS(root)))
	tags := map[string]string{}
	for _, name := range []string{"mermaid.js", "echarts.js", "boot.js"} {
		data, _ := fs.ReadFile(root, name)
		tags[name] = fmt.Sprintf(`"%x"`, sha256.Sum256(data))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tag := tags[strings.TrimPrefix(r.URL.Path, "/_runtime/")]; tag != "" {
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		}
		files.ServeHTTP(w, r)
	})
}()

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
