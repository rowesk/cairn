package cairn

import (
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Adapt links at delivery time so previously saved reports benefit too.
// Preserve all other bytes, including custom styles and scripts.
func reportLinks(content string) string {
	z := html.NewTokenizer(strings.NewReader(content))
	var out strings.Builder
	out.Grow(len(content))
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return content
			}
			return out.String()
		}
		raw := string(z.Raw())
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			out.WriteString(raw)
			continue
		}
		tag := z.Token()
		if tag.Data != "a" && tag.Data != "area" {
			out.WriteString(raw)
			continue
		}
		external := false
		for _, a := range tag.Attr {
			if a.Key == "href" {
				u, err := url.Parse(strings.TrimSpace(a.Val))
				external = err == nil && u.Host != "" && (u.Scheme == "" || strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https"))
			}
		}
		if !external {
			out.WriteString(raw)
			continue
		}
		attrs := make([]html.Attribute, 0, len(tag.Attr)+2)
		rel := "noopener noreferrer"
		for _, a := range tag.Attr {
			if a.Key == "target" {
				continue
			}
			if a.Key == "rel" {
				for _, value := range strings.Fields(a.Val) {
					if !strings.EqualFold(value, "opener") && !strings.EqualFold(value, "noopener") && !strings.EqualFold(value, "noreferrer") {
						rel += " " + value
					}
				}
				continue
			}
			attrs = append(attrs, a)
		}
		tag.Attr = append(attrs, html.Attribute{Key: "target", Val: "_blank"}, html.Attribute{Key: "rel", Val: rel})
		out.WriteString(tag.String())
	}
}
