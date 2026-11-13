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
	"publicPreview": publicPreview,
	"themes":        func() []themePreset { return themePresets },
	"stylesheet":    func() string { return stylesheetURL },
	"fontPreloads":  fontPreloads,
	"scriptURL":     scriptFor,
	"colorScheme":   colorScheme,
	"themeColors":   themeColors,
	"mark": func() template.HTML {
		return template.HTML(`<svg class="mark" viewBox="0 0 30 34" aria-hidden="true"><path d="M11 3c3-3 10-2 10 2 0 3-8 5-11 3C7 7 8 5 11 3ZM7 13c4-3 15-3 17 1 2 4-9 6-15 5-6 0-7-3-2-6ZM5 24c6-3 19-3 22 1 3 5-7 8-17 7-10-1-13-4-5-8Z"/></svg>`)
	},
	"icon":      uiIcon,
	"fileIcon":  fileIcon,
	"fileSize":  formatFileSize,
	"fileType":  fileTypeName,
	"fileShort": fileLabel,
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

// uiIcons are Lucide icons (https://lucide.dev, ISC licence, lucide-static
// 0.468.0), copied in as path data so Cairn needs no icon dependency. The
// comment above each names the Lucide icon, so a replacement comes from the
// same set. Licence: ui/LUCIDE-LICENSE.
var uiIcons = map[string]string{
	// circle-alert
	"alert": `<circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/>`,
	// archive
	"archive": `<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>`,
	// arrow-right
	"arrow": `<path d="M5 12h14"/><path d="m12 5 7 7-7 7"/>`,
	// chevron-left
	"back": `<path d="m15 18-6-6 6-6"/>`,
	// check
	"check": `<path d="M20 6 9 17l-5-5"/>`,
	// chevron-down
	"chevron": `<path d="m6 9 6 6 6-6"/>`,
	// x
	"close": `<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`,
	// copy
	"copy": `<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>`,
	// file-text
	"doc": `<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>`,
	// download
	"download": `<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>`,
	// pencil
	"edit": `<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/>`,
	// sliders-horizontal
	"filter": `<line x1="21" x2="14" y1="4" y2="4"/><line x1="10" x2="3" y1="4" y2="4"/><line x1="21" x2="12" y1="12" y2="12"/><line x1="8" x2="3" y1="12" y2="12"/><line x1="21" x2="16" y1="20" y2="20"/><line x1="12" x2="3" y1="20" y2="20"/><line x1="14" x2="14" y1="2" y2="6"/><line x1="8" x2="8" y1="10" y2="14"/><line x1="16" x2="16" y1="18" y2="22"/>`,
	// circle-help
	"help": `<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>`,
	// info
	"info": `<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`,
	// key-round
	"key": `<path d="M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h1a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h.172a2 2 0 0 0 1.414-.586l.814-.814a6.5 6.5 0 1 0-4-4z"/><circle cx="16.5" cy="7.5" r=".5" fill="currentColor"/>`,
	// keyboard
	"keyboard": `<path d="M10 8h.01"/><path d="M12 12h.01"/><path d="M14 8h.01"/><path d="M16 12h.01"/><path d="M18 8h.01"/><path d="M6 8h.01"/><path d="M7 16h10"/><path d="M8 12h.01"/><rect width="20" height="16" x="2" y="4" rx="2"/>`,
	// book-open-text
	"kindle": `<path d="M12 7v14"/><path d="M16 12h2"/><path d="M16 8h2"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/><path d="M6 12h2"/><path d="M6 8h2"/>`,
	// link
	"link": `<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`,
	// library
	"list": `<path d="m16 6 4 14"/><path d="M12 6v14"/><path d="M8 8v12"/><path d="M4 4v16"/>`,
	// lock
	"lock": `<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>`,
	// ellipsis
	"more": `<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>`,
	// pen-line
	"pen": `<path d="M12 20h9"/><path d="M16.376 3.622a1 1 0 0 1 3.002 3.002L7.368 18.635a2 2 0 0 1-.855.506l-2.872.838a.5.5 0 0 1-.62-.62l.838-2.872a2 2 0 0 1 .506-.854z"/>`,
	// plus
	"plus": `<path d="M5 12h14"/><path d="M12 5v14"/>`,
	// archive-restore
	"restore": `<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h2"/><path d="M20 8v11a2 2 0 0 1-2 2h-2"/><path d="m9 15 3-3 3 3"/><path d="M12 12v9"/>`,
	// search
	"search": `<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`,
	// settings
	"settings": `<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`,
	// share
	"share": `<path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8"/><polyline points="16 6 12 2 8 6"/><line x1="12" x2="12" y1="2" y2="15"/>`,
	// chevron-up
	"up": `<path d="m18 15-6-6-6 6"/>`,
	// upload
	"upload": `<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" x2="12" y1="3" y2="15"/>`,
}

func uiIcon(name string) template.HTML {
	return template.HTML(`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true">` + uiIcons[name] + `</svg>`)
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
