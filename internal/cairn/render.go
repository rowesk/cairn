package cairn

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"golang.org/x/net/html"
)

func preparePublication(in *publication) error {
	if (strings.TrimSpace(in.HTML) == "") == (strings.TrimSpace(in.Markdown) == "") {
		return fmt.Errorf("supply exactly one of html or markdown")
	}
	if in.Template != "" && in.Template != "report" && in.Template != "comparison" && in.Template != "visual" {
		return fmt.Errorf("unknown template")
	}
	if len(in.Markdown) > 2<<20 {
		return fmt.Errorf("markdown exceeds 2 MiB")
	}
	if in.Markdown == "" {
		return nil
	}
	layout := in.Template
	if layout == "" {
		layout = "report"
	}
	var body bytes.Buffer
	if err := goldmark.New(goldmark.WithExtensions(extension.GFM)).Convert([]byte(in.Markdown), &body); err != nil {
		return err
	}
	doc, err := html.Parse(strings.NewReader(body.String()))
	if err != nil {
		return err
	}
	decorateReport(doc)
	var content bytes.Buffer
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				html.Render(&content, c)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}
	extract(doc)
	var output bytes.Buffer
	err = reportTemplate.Execute(&output, struct {
		Title, Layout string
		Body          template.HTML
	}{in.Title, layout, template.HTML(content.String())})
	if err != nil {
		return err
	}
	in.HTML = output.String()
	in.Markdown = ""
	return nil
}
func decorateReport(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		decorateReport(c)
		if c.Type == html.ElementNode && c.Data == "table" {
			wrapNode(n, c, &html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{{Key: "class", Val: "table-scroll"}, {Key: "tabindex", Val: "0"}, {Key: "role", Val: "region"}, {Key: "aria-label", Val: "Scrollable comparison table"}}})
		}
		if c.Type == html.ElementNode && c.Data == "img" && n.Data != "a" {
			for _, a := range c.Attr {
				if a.Key == "src" {
					wrapNode(n, c, &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: a.Val}, {Key: "class", Val: "report-image"}, {Key: "aria-label", Val: "Open image at full size"}}})
					break
				}
			}
		}
		c = next
	}
}
func wrapNode(parent, child, wrapper *html.Node) {
	parent.InsertBefore(wrapper, child)
	parent.RemoveChild(child)
	wrapper.AppendChild(child)
}

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title><style>
:root{color-scheme:light;--ink:#132b38;--muted:#4c6270;--line:#d8e1e6;--accent:#176582}*{box-sizing:border-box}body{margin:0;background:#fff;color:var(--ink);font:18px/1.7 Georgia,serif}main{max-width:46rem;margin:0 auto;padding:2rem 1.25rem 5rem}h1,h2,h3,th{font-family:system-ui,sans-serif;line-height:1.25}h1{font-size:clamp(2rem,5vw,3rem);letter-spacing:-.035em;margin:0 0 1.5rem}h2{font-size:1.5rem;margin:2.5rem 0 1rem}h3{font-size:1.15rem}a{color:var(--accent);text-underline-offset:.2em}a:focus-visible,.table-scroll:focus-visible{outline:3px solid var(--accent);outline-offset:4px}p,li{overflow-wrap:anywhere}blockquote{border-left:3px solid var(--accent);margin:1.5rem 0;padding:.25rem 1.25rem;color:var(--muted)}img{max-width:100%;height:auto;display:block;margin:1rem auto}.report-image{display:block}pre{overflow:auto;padding:1rem;background:#f4f7f9;font-size:.85rem}code{font-size:.9em}.table-scroll{max-width:100%;overflow-x:auto;margin:1.5rem 0;border:1px solid var(--line);border-radius:6px}table{border-collapse:collapse;min-width:100%;font:15px/1.5 system-ui,sans-serif}th,td{padding:.9rem 1.1rem;border-bottom:1px solid var(--line);text-align:left;min-width:8rem}th{background:#eef4f7;font-weight:650}tr:last-child td{border-bottom:0}hr{border:0;border-top:1px solid var(--line);margin:2.5rem 0}[data-template=comparison]{max-width:76rem}[data-template=comparison]>p{max-width:65ch}[data-template=visual]{max-width:88rem}[data-template=visual] img{max-height:80vh;object-fit:contain}[data-template=visual]>p{max-width:65ch}@media(max-width:600px){body{font-size:17px}main{padding:1.25rem 1rem 3rem}th,td{padding:.7rem .8rem}}
</style></head><body><main data-template="{{.Layout}}">{{.Body}}</main></body></html>`))
