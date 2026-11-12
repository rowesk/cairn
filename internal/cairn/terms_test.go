package cairn_test

import (
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Retired words, with what replaced them (CONTEXT.md has the full list).
// Field names, flags, file names and CSS classes keep their spelling; only
// words a person reads are checked.
var retiredTerms = []struct{ pattern, use string }{
	{`(?i)\blink access\b`, "Shared"},
	{`(?i)\bpublic (path|alias)\b`, "public link or link name"},
	{`(?i)\bpublic url name\b`, "link name"},
	{`(?i)\bpublisher token\b`, "installation token"},
	{`(?i)\boriginal prompt\b`, "your request or original request"},
	{`(?i)\badmin view\b`, "your library"},
	{`(?i)\bpeople (&|&amp;|and) agents\b`, "Authors"},
	{`(?i)\bsave themes\b`, "the theme cards"},
	{`(?i)\bomarchy\b`, "what the theme looks like"},
	{`\b(AM|PM)\b`, "24-hour time"},
}

// Words retired from the owner's and visitors' screens, where the API's own
// field names are not the vocabulary.
var retiredOnScreen = []struct{ pattern, use string }{
	{`(?i)\bslugs?\b`, "link name"},
	{`(?i)\breports?\b`, "page"},
	{`(?i)\bpublic page\b`, "shared page"},
	{`\bPublic\b`, "Shared, or public link"},
	{`(?i)\b(by|for|from) (a |an )?person\b`, "author"},
	{`(?i)\bpeople\b`, "authors"},
	{`(?i)\boriginal request\b`, "your request"},
}

func findRetired(t *testing.T, where, text string, lists ...[]struct{ pattern, use string }) {
	t.Helper()
	for _, list := range lists {
		for _, term := range list {
			if m := regexp.MustCompile(term.pattern).FindStringIndex(text); m != nil {
				t.Errorf("%s: %q is retired, use %s: …%s…", where, text[m[0]:m[1]], term.use, text[max(0, m[0]-50):min(len(text), m[1]+50)])
			}
		}
	}
}

var (
	invisible = regexp.MustCompile(`(?is)<(script|style|template|textarea id="agent-setup-guide")[^>]*>.*?</(script|style|template|textarea)>`)
	readable  = regexp.MustCompile(`(?i)\s(aria-label|title|placeholder|alt)="([^"]*)"`)
	tags      = regexp.MustCompile(`<[^>]*>`)
	// The template's own name, the address every example uses, and "Public link" starting a
	// label or sentence are not retired words.
	allowedOnScreen = regexp.MustCompile(`Report · reading and notes|reports\.example\.com|Public links?\b`)
)

// screenText is what a person reads on a page: its text and the labels,
// titles, placeholders and alt text a screen reader or hover shows.
func screenText(page string) string {
	page = invisible.ReplaceAllString(page, " ")
	var labels []string
	for _, m := range readable.FindAllStringSubmatch(page, -1) {
		labels = append(labels, m[2])
	}
	text := tags.ReplaceAllString(page, " ") + " " + strings.Join(labels, " ")
	return allowedOnScreen.ReplaceAllString(html.UnescapeString(text), " ")
}

func TestRetiredTermsStayRetired(t *testing.T) {
	private, public, closeApp := openThemed(t, t.TempDir())
	defer closeApp()
	written := url.Values{"title": {"Draughts, room by room"}, "markdown": {"# Draughts\n\nStart with the letterbox.\n\n## Doors\n\nBrush strips.\n\n## Windows\n\nFoam tape.\n\n## Floors\n\nSilicone."}, "slug": {"draughts"}, "access": {"public"}}
	if code, _, _ := authorForm(t, private, "/new", written, private.URL); code != 303 {
		t.Fatal(code)
	}
	custom := page("Router shortlist")
	custom["slug"], custom["html"] = "router", "<h1>Three routers</h1><p>Buy the Archer.</p>"
	if code, _, _ := request(t, private, "POST", "/api/pages", custom, token); code != 201 {
		t.Fatal(code)
	}
	if code := ownerPost(t, private, "router", url.Values{"action": {"share"}}); code != 303 {
		t.Fatal(code)
	}
	if code := ownerPost(t, private, "router", url.Values{"action": {"password"}, "password": {"warm-kitchen-1"}}); code != 303 {
		t.Fatal(code)
	}
	for _, path := range []string{"/", "/?shared=1", "/?archived=1", "/?q=zzzz", "/?agent=Nobody", "/settings", "/settings?saved=appearance", "/new", "/import", "/manage/draughts", "/manage/router", "/manage/draughts/delete", "/draughts", "/router", "/edit/draughts", "/edit/router", "/no-such-page"} {
		_, _, body := request(t, private, "GET", path, nil, "")
		findRetired(t, "owner "+path, screenText(string(body)), retiredTerms, retiredOnScreen)
	}
	for _, path := range []string{"/draughts", "/router", "/no-such-page"} {
		_, _, body := request(t, public, "GET", path, nil, "")
		findRetired(t, "visitor "+path, screenText(string(body)), retiredTerms, retiredOnScreen)
	}
	// Words the page scripts put on screen.
	scripts, _ := filepath.Glob(filepath.Join("ui", "*.js"))
	prose := regexp.MustCompile(`'((?:[^'\\\n]|\\.)*[A-Za-z] [a-z](?:[^'\\\n]|\\.)*)'`)
	for _, file := range scripts {
		data, _ := os.ReadFile(file)
		source := regexp.MustCompile(`(?m)^\s*//.*$|/\*(?s:.*?)\*/`).ReplaceAllString(string(data), "")
		for _, m := range prose.FindAllStringSubmatch(source, -1) {
			if strings.ContainsAny(m[1], "#.[]{}=;") && !strings.Contains(m[1], ". ") {
				continue // a selector or code, not words
			}
			findRetired(t, file, m[1], retiredTerms, retiredOnScreen)
		}
	}
	// Messages in Go: everything people or agents read. The API and the
	// command line name the slug field and flag by name, so only the screens'
	// own files are held to the screen list.
	screens := map[string]bool{"editor.go": true, "feedback.go": true, "import.go": true, "keys.go": true, "kindle.go": true, "library.go": true, "password.go": true, "reader.go": true, "settings.go": true, "sharing.go": true, "themes.go": true}
	literal := regexp.MustCompile("\"((?:[^\"\\\\\\n]|\\\\.)*)\"")
	sources, _ := filepath.Glob("*.go")
	commands, _ := filepath.Glob(filepath.Join("..", "..", "cmd", "cairn", "*.go"))
	for _, file := range append(sources, commands...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, _ := os.ReadFile(file)
		for _, m := range literal.FindAllStringSubmatch(string(data), -1) {
			words := m[1]
			if !strings.Contains(words, " ") || regexp.MustCompile(`^(SELECT|UPDATE|INSERT|DELETE|CREATE|ALTER|PRAGMA|<|\{)`).MatchString(words) {
				continue
			}
			findRetired(t, file, words, retiredTerms)
			if screens[filepath.Base(file)] && !strings.Contains(words, "class=") {
				findRetired(t, file, words, retiredOnScreen[:1])
			}
		}
	}
	// The documentation, the agent setup text and the unreleased changes.
	docs, _ := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	docs = append(docs, filepath.Join("..", "..", "README.md"), filepath.Join("..", "..", "CONTRIBUTING.md"), "contract.md", filepath.Join("ui", "agent-setup.md"))
	code := regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")
	for _, file := range docs {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		findRetired(t, file, code.ReplaceAllString(string(data), " "), retiredTerms)
	}
	changelog, _ := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	unreleased := strings.SplitN(strings.SplitN(string(changelog), "## Unreleased", 2)[1], "\n## ", 2)[0]
	findRetired(t, "CHANGELOG.md Unreleased", code.ReplaceAllString(unreleased, " "), retiredTerms[:len(retiredTerms)-1])
	glossary, _ := os.ReadFile(filepath.Join("..", "..", "CONTEXT.md"))
	var lines []string
	for _, line := range strings.Split(string(glossary), "\n") {
		if !strings.HasPrefix(line, "|") { // the table lists the retired words on purpose
			lines = append(lines, line)
		}
	}
	findRetired(t, "CONTEXT.md", code.ReplaceAllString(strings.Join(lines, "\n"), " "), retiredTerms)
}
