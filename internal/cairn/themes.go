package cairn

import _ "embed"

//go:embed ui/themes.css
var themesCSS string

type themePreset struct {
	ID, Name, Description string
}

var themePresets = []themePreset{
	{"cairn", "Cairn", "Blue covers, soft edges and the familiar Cairn type."},
	{"minimal", "Minimal", "White space, compact covers and simple black controls."},
	{"futuristic", "Futuristic", "Omarchy-inspired purple greys, violet accents and angular controls."},
}

type themePreferences struct {
	Admin, Reader, Public string
}

func validTheme(id string) bool {
	for _, preset := range themePresets {
		if preset.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) themes() themePreferences {
	p := themePreferences{Admin: "cairn", Reader: "cairn", Public: "cairn"}
	_ = s.db.QueryRow(`SELECT admin,reader,public FROM theme_preferences WHERE id=1`).Scan(&p.Admin, &p.Reader, &p.Public)
	for _, id := range []*string{&p.Admin, &p.Reader, &p.Public} {
		if !validTheme(*id) {
			*id = "cairn"
		}
	}
	return p
}
