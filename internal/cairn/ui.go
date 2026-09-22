package cairn

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"html/template"
	"time"
)

//go:embed ui/owner.css
var ownerBaseCSS string

//go:embed ui/focus.css
var focusCSS string

//go:embed ui/focus.js
var focusJS string

var ownerCSS = ownerBaseCSS + "\n" + focusCSS
var focusScriptTag = "<script>" + focusJS + "</script>"
var focusScriptHash = func() string {
	sum := sha256.Sum256([]byte(focusJS))
	return base64.StdEncoding.EncodeToString(sum[:])
}()

//go:embed ui/library.html
var libraryHTML string

//go:embed ui/library.js
var libraryBodyJS string

var libraryJS = focusJS + "\n" + libraryBodyJS

var libraryScriptHash = func() string {
	sum := sha256.Sum256([]byte(libraryJS))
	return base64.StdEncoding.EncodeToString(sum[:])
}()
var uiFunctions = template.FuncMap{
	"css":    func() template.CSS { return template.CSS(ownerCSS) },
	"script": func() template.JS { return template.JS(libraryJS) },
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
	"access": func(p libraryItem) string {
		if p.Shared {
			if p.Protected {
				return "Password"
			}
			return "Link access"
		}
		return "Private"
	},
}

func uiIcon(name string) template.HTML {
	paths := map[string]string{
		"plus":    `<path d="M12 5v14M5 12h14"/>`,
		"chevron": `<path d="m7 10 5 5 5-5"/>`,
		"list":    `<path d="M8 5h13M8 12h13M8 19h13M3 5h.1M3 12h.1M3 19h.1"/>`,
		"link":    `<path d="m9 15 6-6M8 16l-1 1a4 4 0 0 1-5-5l5-5a4 4 0 0 1 5 0m0 1 1-1a4 4 0 0 1 5 5l-5 5a4 4 0 0 1-5 0"/>`,
		"archive": `<path d="M4 8v12h16V8M9 12h6"/><rect x="3" y="3" width="18" height="5" rx="1"/>`,
		"lock":    `<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/>`,
		"upload":  `<path d="M4 15v6h16v-6M12 16V3m-5 5 5-5 5 5"/>`,
		"search":  `<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4 4"/>`,
		"doc":     `<path d="M6 3h8l4 4v14H6zM14 3v5h4M9 12h6M9 16h5"/>`,
		"more":    `<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`,
		"arrow":   `<path d="M5 12h14m-6-6 6 6-6 6"/>`,
		"close":   `<path d="m6 6 12 12M18 6 6 18"/>`,
	}
	return template.HTML(`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true">` + paths[name] + `</svg>`)
}

var libraryView = template.Must(template.New("library").Funcs(uiFunctions).Parse(libraryHTML))
