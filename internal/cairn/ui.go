package cairn

import (
	_ "embed"
	"hash/fnv"
	"html/template"
	"net/http"
	"strings"
	"time"
)

//go:embed ui/focus.js
var focusJS string

//go:embed ui/layout.html
var layoutHTML string

//go:embed ui/library.html
var libraryHTML string

//go:embed ui/library.js
var libraryBodyJS string

var libraryJS = focusJS + "\n" + libraryBodyJS

// uiAssetPolicy allows the hashed stylesheet, fonts and page scripts under
// /_ui/ and nothing inline: pages carry no <style> blocks, style attributes
// or inline scripts. Every response carries nosniff, so nothing else on
// Cairn's origin runs as a script.
const uiAssetPolicy = "style-src 'self'; font-src 'self'; script-src 'self'"

var uiFunctions = template.FuncMap{
	"themes":       func() []themePreset { return themePresets },
	"stylesheet":   func() string { return stylesheetURL },
	"fontPreloads": fontPreloads,
	"scriptURL":    scriptFor,
	"colorScheme":  colorScheme,
	"themeColors":  themeColors,
	"mark": func() template.HTML {
		return template.HTML(`<svg class="mark" viewBox="0 0 30 34" aria-hidden="true"><path d="M11 3c3-3 10-2 10 2 0 3-8 5-11 3C7 7 8 5 11 3ZM7 13c4-3 15-3 17 1 2 4-9 6-15 5-6 0-7-3-2-6ZM5 24c6-3 19-3 22 1 3 5-7 8-17 7-10-1-13-4-5-8Z"/></svg>`)
	},
	"icon": uiIcon,
	"date": func(s string) string {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Format("2 Jan 2006")
		}
		return s
	},
	"initial": func(s string) string {
		for _, r := range s {
			return string(r)
		}
		return ""
	},
	"tint": avatarTint,
	// shortcutsOff reports whether the owner turned single-key shortcuts
	// off in Settings. Owner page data embeds ownerChrome; anything else
	// (visitor pages) keeps them on.
	"shortcutsOff": func(data any) bool {
		chrome, ok := data.(interface{ shortcutsOff() bool })
		return ok && chrome.shortcutsOff()
	},
}

// ownerChrome is what every owner page's shell needs beyond its theme. Embed
// it in a page's data and fill it with s.chrome().
type ownerChrome struct {
	// ShortcutsOff puts data-shortcuts="off" on <html>, so the page's script
	// leaves keys alone (WCAG 2.1.4).
	ShortcutsOff bool
}

func (c ownerChrome) shortcutsOff() bool { return c.ShortcutsOff }

// chrome reads the owner's shell preferences.
func (s *Server) chrome() ownerChrome {
	return ownerChrome{ShortcutsOff: !s.preferences().Shortcuts}
}

// avatarTints is how many tints initials avatars come in. Each theme defines
// --tint-1-bg/--tint-1-ink to --tint-6-bg/--tint-6-ink in tokens.css.
const avatarTints = 6

// avatarTint gives a name its stable avatar tint, from 1 to avatarTints, so
// two authors who share an initial still look different, the same way on
// every page and after every restart. Case and outer spaces don't count.
func avatarTint(name string) int {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
	return int(h.Sum32()%avatarTints) + 1
}

func uiIcon(name string) template.HTML {
	paths := map[string]string{
		"plus":     `<path d="M12 5v14M5 12h14"/>`,
		"chevron":  `<path d="m7 10 5 5 5-5"/>`,
		"list":     `<path d="M8 5h13M8 12h13M8 19h13M3 5h.1M3 12h.1M3 19h.1"/>`,
		"link":     `<path d="m9 15 6-6M8 16l-1 1a4 4 0 0 1-5-5l5-5a4 4 0 0 1 5 0m0 1 1-1a4 4 0 0 1 5 5l-5 5a4 4 0 0 1-5 0"/>`,
		"archive":  `<path d="M4 8v12h16V8M9 12h6"/><rect x="3" y="3" width="18" height="5" rx="1"/>`,
		"lock":     `<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/>`,
		"upload":   `<path d="M4 15v6h16v-6M12 16V3m-5 5 5-5 5 5"/>`,
		"search":   `<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4 4"/>`,
		"doc":      `<path d="M6 3h8l4 4v14H6zM14 3v5h4M9 12h6M9 16h5"/>`,
		"more":     `<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`,
		"arrow":    `<path d="M5 12h14m-6-6 6 6-6 6"/>`,
		"close":    `<path d="m6 6 12 12M18 6 6 18"/>`,
		"copy":     `<rect x="8" y="8" width="12" height="13" rx="2"/><path d="M15 8V3H3v13h5"/>`,
		"check":    `<path d="m5 12 5 5 9-10"/>`,
		"kindle":   `<rect x="5" y="2" width="14" height="20" rx="2"/><path d="M8.5 6.5h7M8.5 10h7M8.5 13.5h4.5M10.5 18.5h3"/>`,
		"alert":    `<circle cx="12" cy="12" r="9"/><path d="M12 7.5v5.5M12 16.5h.01"/>`,
		"info":     `<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.5h.01"/>`,
		"up":       `<path d="m7 14 5-5 5 5"/>`,
		"key":      `<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3M15 8l2 2"/>`,
		"pen":      `<path d="M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16v4ZM13.5 6.5l4 4"/>`,
		"restore":  `<path d="M4 8v12h16V8M12 17v-6m-3 3 3-3 3 3"/><rect x="3" y="3" width="18" height="5" rx="1"/>`,
		"back":     `<path d="m15 5-7 7 7 7"/>`,
		"settings": `<path d="M10.2 3h3.6l.6 2.3 1.5.9 2.3-.7 1.8 3.1-1.7 1.7v1.4l1.7 1.7-1.8 3.1-2.3-.7-1.5.9-.6 2.3h-3.6l-.6-2.3-1.5-.9-2.3.7-1.8-3.1 1.7-1.7v-1.4L3.8 8.6l1.8-3.1 2.3.7 1.5-.9z"/><circle cx="12" cy="12" r="2.6"/>`,
		"filter":   `<path d="M4 7h10M18 7h2M4 17h4M12 17h8"/><circle cx="16" cy="7" r="2"/><circle cx="10" cy="17" r="2"/>`,
		"download": `<path d="M12 3v12m-5-5 5 5 5-5M4 19h16"/>`,
		"help":     `<circle cx="12" cy="12" r="9"/><path d="M9.6 9.3a2.5 2.5 0 0 1 4.8.9c0 1.7-2.4 2.2-2.4 3.8M12 16.8h.01"/>`,
	}
	return template.HTML(`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true">` + paths[name] + `</svg>`)
}

// layoutView holds the shared shell from ui/layout.html. Pages are parsed on
// a copy of it, so every page gets the same head, skip link, top bar and
// landmarks. See ui/layout.html for the blocks a page can define.
var layoutView = template.Must(template.Must(template.New("layout").Funcs(uiFunctions).Parse(layoutHTML)).Parse(feedbackHTML))

// pageView parses one page template on top of the shared layout. funcs adds
// page-specific helpers, such as the page's own script.
func pageView(name, source string, funcs template.FuncMap) *template.Template {
	t := template.Must(layoutView.Clone())
	if funcs != nil {
		t.Funcs(funcs)
	}
	return template.Must(t.New(name).Parse(source))
}

var libraryView = pageView("library", libraryHTML, nil)

//go:embed ui/import.html
var importHTML string

//go:embed ui/manage.html
var manageHTML string

//go:embed ui/password.html
var passwordHTML string

//go:embed ui/forms.js
var formsBodyJS string

// formsJS adds copy buttons, password show/hide, the import drop zone and
// in-place feedback to the import, manage and password forms. Every one of
// those forms works without it.
var formsJS = focusJS + "\n" + formsBodyJS

var importView = pageView("import", importHTML, nil)

var manageView = pageView("manage", manageHTML, nil)

var passwordView = pageView("password", passwordHTML, nil)

// formPageHeaders are the headers of the import, manage and password pages.
func formPageHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; "+uiAssetPolicy+"; img-src data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}
