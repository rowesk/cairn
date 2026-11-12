package cairn

import (
	"cmp"
	"database/sql"
	"net/http"
	"strings"
)

// themePreset is one look Cairn can wear. Its ID is stored, so IDs never
// change; an ID this build doesn't know falls back to Cairn.
type themePreset struct {
	ID, Name, Description string
}

// themePresets is the order Settings offers them in. System is not a look of
// its own: it shows Cairn or Dark, following the device's setting.
var themePresets = []themePreset{
	{"system", "System", "Cairn by day and Dark by night, following each device’s setting. Visitors get their own."},
	{"cairn", "Cairn", "Blue-grey paper and the navy Blue Hour cover."},
	{"dark", "Dark", "The same blues on deep slate, for reading at night."},
	{"minimal", "Minimal", "Black on white, thin rules and the device’s own fonts."},
	{"futuristic", "Futuristic", "Midnight indigo, violet links and squarer corners."},
}

// defaultTheme is what a new installation starts with. Installations set up
// before themes followed the device keep the theme they saved.
const defaultTheme = "system"

func validTheme(id string) bool {
	for _, preset := range themePresets {
		if preset.ID == id {
			return true
		}
	}
	return false
}

func themeName(id string) string {
	for _, preset := range themePresets {
		if preset.ID == id {
			return preset.Name
		}
	}
	return ""
}

// themePreferences are the installation's theme choices. Admin is the
// library, editor, settings and management pages. Reader and Public are the
// theme of the owner's reader and of everything visitors see; empty means
// "the same as Admin".
type themePreferences struct {
	Admin, Reader, Public string
}

// reader and public resolve "the same as Admin".
func (p themePreferences) reader() string { return cmp.Or(p.Reader, p.Admin) }
func (p themePreferences) public() string { return cmp.Or(p.Public, p.Admin) }

// themes reads the saved choices. An unknown ID falls back: Cairn for the
// library, and the library's theme for the reader and shared pages.
func (s *Server) themes() themePreferences {
	p := themePreferences{Admin: defaultTheme}
	_ = s.db.QueryRow(`SELECT admin,reader,public FROM theme_preferences WHERE id=1`).Scan(&p.Admin, &p.Reader, &p.Public)
	if !validTheme(p.Admin) {
		p.Admin = "cairn"
	}
	for _, id := range []*string{&p.Reader, &p.Public} {
		if !validTheme(*id) {
			*id = ""
		}
	}
	return p
}

// migrateThemes moves installations from three independent themes to one
// theme with optional overrides: a reader or public theme that matched the
// library becomes "the same as the library", so choosing a theme in Settings
// changes every view the owner hasn't set apart. It runs once.
func migrateThemes(db *sql.DB) error {
	return addColumn(db, "theme_preferences", "version", "INTEGER NOT NULL DEFAULT 0", `UPDATE theme_preferences SET reader=CASE WHEN reader=admin THEN '' ELSE reader END, public=CASE WHEN public=admin THEN '' ELSE public END, version=1 WHERE version=0`)
}

// colorScheme is the color-scheme a theme renders in, for the <meta> that
// lets the browser paint its own controls and first frame to match.
func colorScheme(theme string) string {
	switch theme {
	case "dark", "futuristic":
		return "dark"
	case "system":
		return "light dark"
	}
	return "light"
}

// themeColor is the browser's toolbar colour for a theme: its page colour.
// System gets one per scheme.
type themeColorTag struct{ Media, Color string }

func themeColors(theme string) []themeColorTag {
	colours := map[string]string{"cairn": "#eef2f5", "dark": "#111b22", "minimal": "#ffffff", "futuristic": "#1a1b26"}
	if theme == "system" {
		return []themeColorTag{{"(prefers-color-scheme: light)", colours["cairn"]}, {"(prefers-color-scheme: dark)", colours["dark"]}}
	}
	if c, ok := colours[theme]; ok {
		return []themeColorTag{{"", c}}
	}
	return []themeColorTag{{"", colours["cairn"]}}
}

// reportScheme is how a report frame should colour a page that Cairn
// rendered: light, dark, or auto (following the device inside the frame).
// Palette tints a dark report for Futuristic (violet) and turns a light one
// black on white for Minimal (mono). Custom HTML ignores both.
type reportScheme struct{ Scheme, Palette string }

func reportSchemeFor(theme string) reportScheme {
	switch theme {
	case "dark":
		return reportScheme{Scheme: "dark"}
	case "futuristic":
		return reportScheme{Scheme: "dark", Palette: "violet"}
	case "system":
		return reportScheme{Scheme: "auto"}
	case "minimal":
		return reportScheme{Scheme: "light", Palette: "mono"}
	}
	return reportScheme{}
}

// query is the report frame's query string, empty for light reports.
func (rs reportScheme) query() string {
	if rs.Scheme == "" {
		return ""
	}
	q := "?scheme=" + rs.Scheme
	if rs.Palette != "" {
		q += "&palette=" + rs.Palette
	}
	return q
}

// reportSchemeOf reads a frame request's scheme. Only known values pass.
func reportSchemeOf(r *http.Request) reportScheme {
	var rs reportScheme
	switch r.URL.Query().Get("scheme") {
	case "dark", "auto", "light":
		rs.Scheme = r.URL.Query().Get("scheme")
	}
	switch palette := r.URL.Query().Get("palette"); {
	case palette == "violet" && (rs.Scheme == "dark" || rs.Scheme == "auto"):
		rs.Palette = palette
	case palette == "mono" && rs.Scheme == "light":
		rs.Palette = palette
	}
	return rs
}

// apply marks a report document's root with its scheme, so Cairn's report
// stylesheet paints the right colours from the first frame, with or without
// JavaScript. Documents without an <html> tag are left alone.
func (rs reportScheme) apply(doc string) string {
	if rs.Scheme == "" || (rs.Scheme == "light" && rs.Palette == "") {
		return doc
	}
	lower := strings.ToLower(doc[:min(len(doc), 4096)])
	at := strings.Index(lower, "<html")
	if at < 0 || at+5 >= len(doc) || !strings.ContainsRune(" \t\r\n>", rune(doc[at+5])) {
		return doc
	}
	attrs := ` data-report-scheme="` + rs.Scheme + `"`
	if rs.Palette != "" {
		attrs += ` data-report-palette="` + rs.Palette + `"`
	}
	return doc[:at+5] + attrs + doc[at+5:]
}

// adaptsToScheme reports whether a stored page carries Cairn's report
// stylesheet with its dark colours. Older pages and custom HTML keep their
// own design in every theme.
func adaptsToScheme(page string) bool {
	return strings.Contains(page, "data-report-scheme")
}
