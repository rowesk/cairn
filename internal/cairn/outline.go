package cairn

import (
	"io"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// The reader builds its contents from the same delivery pass that gives the
// report's headings their ids, so every contents link has a target.

const outlineLimit = 300

type outlineHeading struct {
	Level    int
	ID, Text string
}

type contentsEntry struct {
	ID, Text, Href string
	Children       []*contentsEntry
}

var headingLevels = map[string]int{"h1": 1, "h2": 2, "h3": 3, "h4": 4, "h5": 5, "h6": 6}

// Headings in these elements belong to report furniture, not its sections.
var outlineSkipped = map[string]bool{"nav": true, "aside": true, "footer": true, "template": true}

// reportHeadings gives each heading without an id a GitHub-style one and
// returns the rewritten report with the headings in document order.
func reportHeadings(content string) (string, []outlineHeading) {
	taken := map[string]bool{}
	z := html.NewTokenizer(strings.NewReader(content))
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return content, nil
			}
			break
		}
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			for _, a := range z.Token().Attr {
				if a.Key == "id" {
					taken[a.Val] = true
				}
			}
		}
	}

	// Buffer each heading until it closes: a missing id comes from its text.
	z = html.NewTokenizer(strings.NewReader(content))
	var out, inner, text strings.Builder
	out.Grow(len(content) + 256)
	var headings []outlineHeading
	var start html.Token
	startRaw, open, needsID, listed, level := "", "", false, false, 0
	skipped, inScript := 0, false
	flush := func(end string) {
		label := strings.Join(strings.Fields(text.String()), " ")
		id := ""
		for _, a := range start.Attr {
			if a.Key == "id" {
				id = strings.TrimSpace(a.Val)
			}
		}
		if needsID {
			id = uniqueID(headingSlug(label), taken)
			attrs := make([]html.Attribute, 0, len(start.Attr)+1)
			for _, a := range start.Attr {
				if a.Key != "id" {
					attrs = append(attrs, a)
				}
			}
			start.Attr = append(attrs, html.Attribute{Key: "id", Val: id})
			startRaw = start.String()
		}
		if listed && label != "" {
			headings = append(headings, outlineHeading{Level: level, ID: id, Text: clip(label, 160)})
		}
		out.WriteString(startRaw)
		out.WriteString(inner.String())
		out.WriteString(end)
		open = ""
	}
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return content, nil
			}
			if open != "" {
				flush("")
			}
			return out.String(), headings
		}
		raw := string(z.Raw())
		var token html.Token
		if kind == html.StartTagToken || kind == html.EndTagToken {
			token = z.Token()
			tag := token.Data
			switch {
			case outlineSkipped[tag] && kind == html.StartTagToken:
				skipped++
			case outlineSkipped[tag] && skipped > 0:
				skipped--
			case tag == "script" || tag == "style":
				inScript = kind == html.StartTagToken
			}
			if kind == html.EndTagToken && tag == open {
				flush(raw)
				continue
			}
		}
		if open != "" {
			if kind == html.TextToken && !inScript {
				text.WriteString(html.UnescapeString(raw))
				text.WriteByte(' ')
			}
			inner.WriteString(raw)
			continue
		}
		if kind == html.StartTagToken {
			start = token
			if n := headingLevels[start.Data]; n > 0 {
				hidden, id := false, ""
				for _, a := range start.Attr {
					switch {
					case a.Key == "id":
						id = strings.TrimSpace(a.Val)
					case a.Key == "hidden", a.Key == "aria-hidden" && a.Val == "true":
						hidden = true
					}
				}
				open, startRaw, level, needsID = start.Data, raw, n, id == ""
				listed = skipped == 0 && !hidden && len(headings) < outlineLimit
				inner.Reset()
				text.Reset()
				continue
			}
		}
		out.WriteString(raw)
	}
}

func headingSlug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_', r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}

func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

func uniqueID(base string, taken map[string]bool) string {
	id := base
	for i := 1; taken[id]; i++ {
		id = base + "-" + strconv.Itoa(i)
	}
	taken[id] = true
	return id
}

// reportContents nests headings under the nearest shallower heading, so a
// skipped level never leaves a gap. A report's lone opening h1 repeats the
// page title, which the cover already shows. Fewer than three entries is not
// worth a contents list.
func reportContents(headings []outlineHeading, route string) []*contentsEntry {
	ones := 0
	for _, h := range headings {
		if h.Level == 1 {
			ones++
		}
	}
	if ones == 1 && len(headings) > 0 && headings[0].Level == 1 {
		headings = headings[1:]
	}
	if len(headings) < 3 {
		return nil
	}
	type branch struct {
		level int
		entry *contentsEntry
	}
	var root []*contentsEntry
	var stack []branch
	for _, h := range headings {
		for len(stack) > 0 && stack[len(stack)-1].level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		entry := &contentsEntry{ID: h.ID, Text: h.Text, Href: "/_content/" + route + "#" + h.ID}
		if len(stack) == 0 {
			root = append(root, entry)
		} else {
			parent := stack[len(stack)-1].entry
			parent.Children = append(parent.Children, entry)
		}
		stack = append(stack, branch{h.Level, entry})
	}
	return root
}
