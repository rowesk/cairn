package cairn

import (
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// Markdown may carry a little raw HTML for things Markdown cannot say, such as
// a line break in a table cell or a keyboard key. Everything else in it is
// removed and reported, so a publisher learns what did not survive.

// sanitizeDropped elements are removed with everything inside them.
var sanitizeDropped = map[string]bool{
	"script": true, "style": true, "iframe": true, "frame": true, "frameset": true, "object": true, "embed": true,
	"applet": true, "template": true, "noscript": true, "noembed": true, "noframes": true, "xmp": true, "plaintext": true,
	"textarea": true, "select": true, "option": true, "optgroup": true, "button": true, "datalist": true, "output": true,
	"canvas": true, "video": true, "audio": true, "source": true, "track": true, "picture": true, "map": true, "area": true,
	"link": true, "meta": true, "base": true, "title": true, "head": true, "dialog": true, "portal": true, "slot": true,
	"svg": true, "math": true, "param": true, "fencedframe": true, "search": true,
}

// sanitizeAllowed maps each allowed element to the attributes it may keep,
// beyond the global ones.
var sanitizeAllowed = map[string]map[string]bool{
	// Text-level
	"a": {"href": true, "name": true, "hreflang": true}, "abbr": {}, "b": {}, "bdi": {}, "bdo": {}, "br": {},
	"cite": {}, "code": {}, "data": {"value": true}, "del": {"cite": true, "datetime": true}, "dfn": {}, "em": {},
	"i": {}, "ins": {"cite": true, "datetime": true}, "kbd": {}, "mark": {}, "q": {"cite": true}, "s": {},
	"samp": {}, "small": {}, "span": {}, "strong": {}, "sub": {}, "sup": {}, "time": {"datetime": true}, "u": {},
	"var": {}, "wbr": {},
	"img": {"src": true, "alt": true, "width": true, "height": true, "loading": true, "decoding": true},
	// Blocks
	"p": {}, "div": {}, "blockquote": {"cite": true}, "pre": {}, "hr": {}, "ul": {}, "ol": {"start": true, "reversed": true, "type": true},
	"li": {"value": true}, "dl": {}, "dt": {}, "dd": {}, "h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {},
	"figure": {}, "figcaption": {}, "details": {"open": true}, "summary": {}, "section": {}, "aside": {},
	"table": {}, "caption": {}, "colgroup": {"span": true}, "col": {"span": true}, "thead": {}, "tbody": {}, "tfoot": {},
	"tr": {}, "th": {"colspan": true, "rowspan": true, "scope": true, "headers": true, "abbr": true, "align": true},
	"td": {"colspan": true, "rowspan": true, "headers": true, "align": true},
	// Task list items from Markdown. Only disabled checkboxes survive.
	"input": {"type": true, "checked": true, "disabled": true},
}

var sanitizeGlobal = map[string]bool{"id": true, "class": true, "title": true, "lang": true, "dir": true, "role": true, "aria-label": true, "aria-hidden": true, "aria-describedby": true, "aria-labelledby": true}

var (
	sanitizeNumber = regexp.MustCompile(`^[0-9]{1,5}$`)
	sanitizeSize   = regexp.MustCompile(`^[0-9]{1,5}(px|%)?$`)
	sanitizeScheme = regexp.MustCompile(`^([a-z][a-z0-9+.-]*):`)
	sanitizeImage  = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp|avif);base64,`)
)

// dropLog collects what sanitizing removed.
type dropLog struct{ tags, attributes map[string]bool }

func (d *dropLog) tag(name string) {
	if d.tags == nil {
		d.tags = map[string]bool{}
	}
	d.tags[name] = true
}

func (d *dropLog) attribute(name string) {
	if d.attributes == nil {
		d.attributes = map[string]bool{}
	}
	d.attributes[name] = true
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sanitizeChildren cleans n's children in place. Disallowed containers are
// unwrapped so their text survives; dangerous ones go with their content.
func sanitizeChildren(n *html.Node, log *dropLog) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.TextNode:
		case html.ElementNode:
			name := strings.ToLower(c.Data)
			allowed, ok := sanitizeAllowed[name]
			switch {
			case c.Namespace != "" || sanitizeDropped[name]:
				log.tag(name)
				n.RemoveChild(c)
			case name == "input" && !checkboxOnly(c):
				log.tag(name)
				n.RemoveChild(c)
			case !ok:
				log.tag(name)
				sanitizeChildren(c, log)
				for gc := c.FirstChild; gc != nil; {
					following := gc.NextSibling
					c.RemoveChild(gc)
					n.InsertBefore(gc, c)
					gc = following
				}
				n.RemoveChild(c)
			default:
				c.Attr = sanitizeAttributes(name, c.Attr, allowed, log)
				if name == "input" {
					c.Attr = append(withoutAttr(c.Attr, "disabled"), html.Attribute{Key: "disabled", Val: ""})
				}
				sanitizeChildren(c, log)
			}
		default:
			// Comments, doctypes and anything else carry nothing a reader sees.
			n.RemoveChild(c)
		}
		c = next
	}
}

func checkboxOnly(n *html.Node) bool {
	return strings.EqualFold(attr(n, "type"), "checkbox")
}

func sanitizeAttributes(element string, attrs []html.Attribute, allowed map[string]bool, log *dropLog) []html.Attribute {
	kept := attrs[:0]
	seen := map[string]bool{}
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		switch {
		case seen[key]:
			continue
		case a.Namespace == "" && (key == "target" || key == "rel"):
			// Cairn sets these on external links when it serves the page.
			continue
		case a.Namespace != "" || !(allowed[key] || sanitizeGlobal[key]) || !sanitizeValue(element, key, a.Val):
			log.attribute(key)
			continue
		}
		seen[key] = true
		kept = append(kept, html.Attribute{Key: key, Val: a.Val})
	}
	return kept
}

func sanitizeValue(element, key, value string) bool {
	switch key {
	case "href", "cite":
		return safeURL(value, false)
	case "src":
		return safeURL(value, true)
	case "width", "height":
		return sanitizeSize.MatchString(strings.TrimSpace(value))
	case "colspan", "rowspan", "span", "start", "value":
		return sanitizeNumber.MatchString(strings.TrimSpace(strings.TrimPrefix(value, "-")))
	case "align":
		switch strings.ToLower(value) {
		case "left", "right", "center":
			return true
		}
		return false
	case "dir":
		switch strings.ToLower(value) {
		case "ltr", "rtl", "auto":
			return true
		}
		return false
	case "type":
		if element == "ol" {
			return value == "1" || value == "a" || value == "A" || value == "i" || value == "I"
		}
		return strings.EqualFold(value, "checkbox")
	case "loading":
		return value == "lazy" || value == "eager"
	case "decoding":
		return value == "async" || value == "sync" || value == "auto"
	case "scope":
		switch value {
		case "row", "col", "rowgroup", "colgroup":
			return true
		}
		return false
	case "role":
		switch value {
		case "doc-noteref", "doc-backlink", "doc-endnotes", "doc-footnote", "note", "presentation", "none":
			return true
		}
		return false
	}
	return true
}

// safeURL accepts relative addresses, web and mail links, and inline raster
// images for img. Browsers ignore tabs, newlines and control characters inside
// a scheme, so they are removed before the check.
func safeURL(value string, image bool) bool {
	clean := strings.Map(func(r rune) rune {
		if r <= 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToLower(value))
	if strings.HasPrefix(clean, "//") {
		return !image
	}
	match := sanitizeScheme.FindStringSubmatch(clean)
	if match == nil {
		return true
	}
	switch match[1] {
	case "http", "https":
		return true
	case "mailto", "tel":
		return !image
	case "data":
		return image && sanitizeImage.MatchString(clean)
	}
	return false
}
