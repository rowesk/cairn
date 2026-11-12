package cairn

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type preferences struct {
	Author, Template string
	LinkLength       int
	// Shortcuts turns Cairn's own keyboard shortcuts on: single keys in the
	// library and reader, Command or Control S and P in the editor.
	Shortcuts bool
}
type authorProfile struct {
	Name                string
	Photo               template.URL
	KeyActive           bool
	KeyCreated, KeyUsed string
	// The key's dates as people say them, and in full for hover.
	KeyCreatedLabel, KeyCreatedFull string
	KeyUsedLabel, KeyUsedFull       string
}
type settingsData struct {
	ownerChrome
	// Notice confirms the last save, or says why it didn't happen.
	Notice      *notice
	Theme       string
	Themes      themePreferences
	CSRF        formToken
	Preferences preferences
	// LinkLength is the link length field as typed, so a rejected value
	// shows again for correction.
	LinkLength    string
	Profiles      []authorProfile
	KindleAddress string
	// KindleWarning says when the saved address isn't one of Amazon's.
	KindleWarning string
	MailFrom      string
	// OwnerName is the default author: the owner's own byline, which never
	// gets an agent key.
	OwnerName string
	// Notices sit in the section they concern: appearance, writing, kindle
	// or people. Errors sit beside their field.
	Notices map[string]*notice
	Errors  fieldErrors
	// SetupOrigin is the private base URL the setup text names, or empty
	// to let the browser use the address Settings was opened at.
	SetupOrigin string
}

// settingsSaved confirms what was saved, in that section.
var settingsSaved = map[string]struct{ section, text string }{
	"writing":        {"writing", "Defaults saved. Your next page uses them."},
	"keyboard-on":    {"keyboard", "Keyboard shortcuts are on."},
	"keyboard-off":   {"keyboard", "Keyboard shortcuts are off. Cairn leaves every key to your browser and your tools."},
	"kindle":         {"kindle", "Kindle address saved."},
	"kindle-removed": {"kindle", "Kindle address removed. Send to Kindle is off until you add one."},
	"photo":          {"people", "Photo saved."},
	"photo-removed":  {"people", "Photo removed. Their initials show instead."},
}

// themeChoice reads the appearance form: one theme for every view, and
// optionally a theme of the reader's own and one for shared pages, where
// empty means "the same". Forms from before the cards sent admin_theme,
// reader_theme and public_theme, each a theme; they still work.
func themeChoice(r *http.Request) (themePreferences, bool) {
	p := themePreferences{Admin: r.FormValue("theme"), Reader: r.FormValue("reader_theme"), Public: r.FormValue("public_theme")}
	if p.Admin == "" && r.Form.Has("admin_theme") {
		p.Admin = r.FormValue("admin_theme")
		if !validTheme(p.Reader) || !validTheme(p.Public) {
			return p, false
		}
	}
	if p.Reader == p.Admin {
		p.Reader = ""
	}
	if p.Public == p.Admin {
		p.Public = ""
	}
	ok := validTheme(p.Admin) && (p.Reader == "" || validTheme(p.Reader)) && (p.Public == "" || validTheme(p.Public))
	return p, ok
}

// themeSummary says which theme each view now uses, in one sentence.
func themeSummary(p themePreferences) string {
	name := themeName(p.Admin)
	if p.Reader == "" && p.Public == "" {
		if p.Admin == "system" {
			return "System is on everywhere. Each device shows Cairn or Dark to match its own setting, visitors’ included."
		}
		return name + " is on everywhere, shared pages included."
	}
	text := name + " is on for your library."
	if p.Reader != "" {
		text += " Pages you open use " + themeName(p.Reader) + "."
	}
	if p.Public != "" {
		text += " Shared pages use " + themeName(p.Public) + "."
	}
	return text
}

// answerThemes replies to the appearance form's own fetch, which applies a
// theme as soon as it is chosen.
func (s *Server) answerThemes(w http.ResponseWriter, p themePreferences, status int, stale bool, err error) {
	reply := map[string]any{"ok": false}
	switch {
	case status != http.StatusOK:
		reply["message"] = "Choose one of the listed themes."
	case stale:
		status = http.StatusForbidden
		reply["message"] = "This page had expired, so the theme didn’t change. Reload Settings and choose it again."
	case err != nil:
		status = http.StatusInternalServerError
		reply["message"] = "Cairn couldn’t save the theme. Its storage didn’t answer; try again in a moment."
	default:
		reply = map[string]any{"ok": true, "message": themeSummary(p), "theme": p.Admin, "reader": p.reader(), "public": p.public()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(reply)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// settingsSections maps each form action to the section it belongs to.
var settingsSections = map[string]string{"themes": "appearance", "shortcuts": "keyboard", "defaults": "writing", "kindle": "kindle", "photo": "people", "remove_photo": "people"}

// kindleWarning is said beside a Kindle address that isn't one of Amazon's.
// Cairn still saves it: forwarding addresses and other services exist.
const kindleWarning = "Amazon’s Kindle addresses end in @kindle.com or @free.kindle.com. Check this one before you send a page."

// kindleAddressValid accepts one plain address whose domain has a dot and a
// top-level part, as every address Amazon or a mail provider gives out does.
// The mail parser alone takes "chris@kindle", which can never be delivered.
func kindleAddressValid(address string) bool {
	if !bareAddress(address) {
		return false
	}
	domain := address[strings.LastIndexByte(address, '@')+1:]
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return false
	}
	dot := strings.LastIndexByte(domain, '.')
	return dot > 0 && len(domain)-dot-1 >= 2
}

// kindleLike reports whether an address looks like a Send to Kindle address.
func kindleLike(address string) bool {
	address = strings.ToLower(address)
	return strings.HasSuffix(address, "@kindle.com") || strings.HasSuffix(address, "@free.kindle.com")
}

// answerSaved replies to a settings field saved by the page's own fetch:
// what was saved, or each field's problem in the words the page shows.
func answerSaved(w http.ResponseWriter, status int, stale bool, err error, errors fieldErrors, message, warning string) {
	reply := map[string]any{"ok": false}
	switch {
	case status != http.StatusOK:
		reply["errors"] = errors
		reply["message"] = "Check the field marked in red."
	case stale:
		status = http.StatusForbidden
		reply["message"] = "This page had expired, so nothing was saved. Reload Settings and try again."
	case err != nil:
		status = http.StatusInternalServerError
		reply["message"] = "Cairn couldn’t save that. Its storage didn’t answer; try again in a moment."
	default:
		reply = map[string]any{"ok": true, "message": message}
		if warning != "" {
			reply["warning"] = warning
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(reply)
}

//go:embed ui/settings.html
var settingsHTML string

//go:embed contract.md
var agentContract string

// setupMarker separates contract.md's maintainer notes from the text agents
// receive. Nothing above it is ever served.
const setupMarker = "<!-- agent setup text starts here -->\n"

// agentSetupGuide is the text Settings copies for an agent, with {{ORIGIN}}
// and {{AUTHOR}} left for the browser to fill. ui/agent-setup.md is a
// checked copy of it for people reading the repository.
var agentSetupGuide = func() string {
	_, text, found := strings.Cut(agentContract, setupMarker)
	if !found {
		panic("cairn: contract.md has no agent setup marker")
	}
	return text
}()

//go:embed ui/settings.js
var settingsBodyJS string
var settingsJS = focusJS + "\n" + settingsBodyJS
var settingsView = pageView("settings", settingsHTML, template.FuncMap{
	"setupGuide": func() string { return agentSetupGuide },
	// sameName matches names the way key rules do: case and outer spaces
	// don't count.
	"sameName":    func(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) },
	"kindleGuide": func() string { return kindleGuide },
})

// hasActiveKey reports whether an agent key is active on a name, ignoring
// letter case.
func (s *Server) hasActiveKey(name string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM agent_keys WHERE lower(name)=lower(?) AND digest IS NOT NULL`, strings.TrimSpace(name)).Scan(&n)
	return n > 0
}

func (s *Server) preferences() preferences {
	p := preferences{Author: "Owner", Template: "report", LinkLength: 3, Shortcuts: true}
	_ = s.db.QueryRow(`SELECT author,template,link_length FROM preferences WHERE id=1`).Scan(&p.Author, &p.Template, &p.LinkLength)
	_ = s.db.QueryRow(`SELECT shortcuts FROM keyboard_preferences WHERE id=1`).Scan(&p.Shortcuts)
	return p
}

// migrateShortcutPreference adds the keyboard shortcut switch, on for every
// existing installation. It has a table of its own, so preferences keeps the
// four columns older versions insert into by position and a downgrade still
// starts.
func migrateShortcutPreference(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS keyboard_preferences (id INTEGER PRIMARY KEY CHECK(id=1),shortcuts INTEGER NOT NULL DEFAULT 1 CHECK(shortcuts IN (0,1)));
 INSERT OR IGNORE INTO keyboard_preferences(id) VALUES(1);`)
	return err
}

func photoURL(data []byte) template.URL {
	if len(data) == 0 {
		return ""
	}
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(data))
}
func (s *Server) authorPhoto(name string) template.URL {
	var data []byte
	_ = s.db.QueryRow(`SELECT photo FROM author_profiles WHERE name=?`, name).Scan(&data)
	return photoURL(data)
}
func (s *Server) profiles() ([]authorProfile, error) {
	rows, err := s.db.Query(`SELECT names.name,p.photo,k.digest IS NOT NULL,COALESCE(k.created_at,''),COALESCE(k.last_used,'') FROM (SELECT agent AS name FROM pages UNION SELECT name FROM author_profiles UNION SELECT author AS name FROM preferences UNION SELECT name FROM agent_keys) names LEFT JOIN author_profiles p ON p.name=names.name LEFT JOIN agent_keys k ON k.name=names.name ORDER BY names.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []authorProfile
	now := time.Now()
	for rows.Next() {
		var name string
		var photo []byte
		var active bool
		var created, used string
		if err := rows.Scan(&name, &photo, &active, &created, &used); err != nil {
			return nil, err
		}
		profile := authorProfile{Name: name, Photo: photoURL(photo), KeyActive: active, KeyCreated: created, KeyUsed: used}
		if active {
			profile.KeyCreatedLabel, profile.KeyCreatedFull = relativeDate(created, now)
			if used != "" {
				profile.KeyUsedLabel, profile.KeyUsedFull = relativeDate(used, now)
			}
			profile.KeyCreatedLabel = lowerFirst(profile.KeyCreatedLabel)
			profile.KeyUsedLabel = lowerFirst(profile.KeyUsedLabel)
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

// lowerFirst lets a relative date sit mid-sentence: "last used yesterday".
func lowerFirst(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func normalizedPhoto(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return nil, fmt.Errorf("Use a PNG, JPEG or GIF photo up to 4,096 pixels on each side.")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("Cairn couldn’t read that photo. Try a PNG or JPEG.")
	}
	bounds := src.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	x0 := bounds.Min.X + (bounds.Dx()-side)/2
	y0 := bounds.Min.Y + (bounds.Dy()-side)/2
	dst := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			dst.Set(x, y, src.At(x0+(2*x+1)*side/256, y0+(2*y+1)*side/256))
		}
	}
	var out bytes.Buffer
	err = png.Encode(&out, dst)
	return out.Bytes(), err
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		s.methodNotAllowed(w, "GET, HEAD, POST")
		return
	}
	themes := s.themes()
	prefs := s.preferences()
	d := settingsData{ownerChrome: s.chrome(), Theme: themes.Admin, Themes: themes, CSRF: s.formToken(), Preferences: prefs, LinkLength: strconv.Itoa(prefs.LinkLength), KindleAddress: s.kindleAddress(), Notices: map[string]*notice{}, Errors: fieldErrors{}, SetupOrigin: strings.TrimRight(s.cfg.PrivateBaseURL, "/")}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
		var err error
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			err = r.ParseMultipartForm(5 << 20)
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
		} else {
			err = r.ParseForm()
		}
		action := r.FormValue("action")
		section := settingsSections[action]
		switch {
		case !sameOrigin(r):
			s.showError(w, http.StatusForbidden, problemForeignForm)
			return
		case err != nil:
			d.Notices["people"] = failed("That photo is too large or couldn’t be read. Use one under 4 MB.")
			status = http.StatusBadRequest
		case !s.freshToken(r) && strings.HasPrefix(action, "key_"):
			// The key dialog reads this text and shows it in place.
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "This page had expired, so the key didn’t change. Reload Settings and try again.")
			return
		case action == "key_create" || action == "key_rotate" || action == "key_revoke":
			s.changeAgentKey(w, r)
			return
		case section == "":
			s.showError(w, http.StatusBadRequest, problem{Title: "Cairn didn’t recognise that change", Message: "Nothing was saved. Go back to Settings and try again.", Action: "Back to Settings", Href: "/settings"})
			return
		default:
			saved := section
			stale := !s.freshToken(r)
			switch action {
			case "themes":
				next, ok := themeChoice(r)
				if !ok {
					d.Notices[section] = failed("Choose one of the listed themes.")
					status = http.StatusBadRequest
					break
				}
				d.Themes = next
				d.Theme = next.Admin
				if !stale {
					_, err = s.db.Exec(`UPDATE theme_preferences SET admin=?,reader=?,public=? WHERE id=1`, next.Admin, next.Reader, next.Public)
				}
			case "shortcuts":
				on := r.FormValue("shortcuts") == "on"
				d.Preferences.Shortcuts, d.ShortcutsOff = on, !on
				saved = "keyboard-off"
				if on {
					saved = "keyboard-on"
				}
				if !stale {
					_, err = s.db.Exec(`UPDATE keyboard_preferences SET shortcuts=? WHERE id=1`, on)
				}
			case "kindle":
				address := strings.TrimSpace(r.FormValue("kindle_address"))
				d.KindleAddress = address
				if address != "" && !kindleAddressValid(address) {
					d.Errors["kindle_address"] = "Enter one email address, such as name@kindle.com."
					status = http.StatusBadRequest
					break
				}
				if address == "" {
					saved = "kindle-removed"
				} else if !kindleLike(address) {
					saved = "kindle-unusual"
				}
				if !stale {
					_, err = s.db.Exec(`UPDATE kindle_preferences SET address=? WHERE id=1`, address)
				}
			case "defaults":
				author := strings.TrimSpace(r.FormValue("author"))
				kind := r.FormValue("template")
				d.Preferences.Author, d.Preferences.Template, d.LinkLength = author, kind, r.FormValue("link_length")
				length, e := strconv.Atoi(d.LinkLength)
				switch {
				case author == "":
					d.Errors["author"] = "Enter the name to use when a page has no author."
				case len(author) > 100 || !utf8.ValidString(author):
					d.Errors["author"] = "Shorten the name to 100 characters or fewer."
				}
				if d.Errors["author"] == "" && author != "" && !strings.EqualFold(author, strings.TrimSpace(prefs.Author)) && s.hasActiveKey(author) {
					// Pages the owner writes would carry that agent's byline,
					// and its key would reach them.
					d.Errors["author"] = "An agent key already uses this name. Choose a name of your own, so agent keys never reach your pages."
				}
				if kind != "report" && kind != "comparison" && kind != "visual" {
					d.Errors["template"] = "Choose one of the listed templates."
				}
				if e != nil || length < 3 || length > 7 {
					d.Errors["link_length"] = "Choose a length from 3 to 7 characters."
				}
				if len(d.Errors) > 0 {
					status = http.StatusBadRequest
					break
				}
				if !stale {
					_, err = s.db.Exec(`UPDATE preferences SET author=?,template=?,link_length=? WHERE id=1`, author, kind, length)
				}
			case "photo", "remove_photo":
				name := strings.TrimSpace(r.FormValue("name"))
				if name == "" || len(name) > 100 || !utf8.ValidString(name) {
					d.Notices[section] = failed("Enter the author’s name exactly as it appears on their pages, up to 100 characters.")
					status = http.StatusBadRequest
					break
				}
				if action == "remove_photo" {
					saved = "photo-removed"
					if !stale {
						_, err = s.db.Exec(`DELETE FROM author_profiles WHERE name=?`, name)
					}
					break
				}
				saved = "photo"
				file, _, e := r.FormFile("photo")
				if e != nil {
					d.Notices[section] = failed("Choose a photo first.")
					status = http.StatusBadRequest
					break
				}
				data, e := io.ReadAll(io.LimitReader(file, (4<<20)+1))
				file.Close()
				if e != nil || len(data) > 4<<20 {
					d.Notices[section] = failed("Use a photo of 4 MB or less.")
					status = http.StatusBadRequest
					break
				}
				data, e = normalizedPhoto(data)
				if e != nil {
					d.Notices[section] = failed(e.Error())
					status = http.StatusBadRequest
					break
				}
				if !stale {
					_, err = s.db.Exec(`INSERT INTO author_profiles(name,photo) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET photo=excluded.photo`, name, data)
				}
			}
			if action == "themes" && wantsJSON(r) {
				s.answerThemes(w, d.Themes, status, stale, err)
				return
			}
			if wantsJSON(r) && (action == "defaults" || action == "kindle" || action == "shortcuts") {
				said := settingsSaved[saved]
				warning := ""
				if saved == "kindle-unusual" {
					said, warning = settingsSaved["kindle"], kindleWarning
				}
				answerSaved(w, status, stale, err, d.Errors, said.text, warning)
				return
			}
			switch {
			case status != http.StatusOK:
			case stale:
				// Cairn's own form with an old token: show it again as typed.
				d.Notices[section] = informed(expiredForm)
				status = http.StatusForbidden
			case err != nil:
				d.Notices[section] = failed("Cairn couldn’t save that. Its storage didn’t answer; try again in a moment.")
				status = http.StatusInternalServerError
			default:
				http.Redirect(w, r, "/settings?saved="+saved+"#"+section, http.StatusSeeOther)
				return
			}
		}
	}
	if saved, ok := settingsSaved[r.URL.Query().Get("saved")]; ok && r.Method != http.MethodPost {
		d.Notices[saved.section] = succeeded(saved.text)
	}
	if r.URL.Query().Get("saved") == "kindle-unusual" && r.Method != http.MethodPost {
		// The warning itself shows once, under the field.
		d.Notices["kindle"] = succeeded(settingsSaved["kindle"].text)
	}
	if d.KindleAddress != "" && !kindleLike(d.KindleAddress) && d.Errors["kindle_address"] == "" {
		d.KindleWarning = kindleWarning
	}
	if r.URL.Query().Get("saved") == "appearance" && r.Method != http.MethodPost {
		d.Notices["appearance"] = succeeded(themeSummary(d.Themes))
	}
	if r.URL.Query().Get("kindle") == "missing" && r.Method != http.MethodPost {
		d.Notices["kindle"] = informed("Add your Kindle address here, then send the page again.")
	}
	if s.cfg.Mail.enabled() {
		d.MailFrom = s.cfg.Mail.From
	}
	var err error
	d.Profiles, err = s.profiles()
	if err != nil && err != sql.ErrNoRows {
		s.showError(w, http.StatusInternalServerError, problemStorage("/settings"))
		return
	}
	for _, section := range []string{"people", "appearance", "keyboard", "writing", "kindle"} {
		if d.Notice == nil && d.Notices[section] != nil {
			d.Notice = d.Notices[section]
		}
	}
	d.OwnerName = prefs.Author
	privateFormHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; "+uiAssetPolicy+"; img-src data: blob:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_ = settingsView.Execute(w, d)
}

// Rebuild the former range constraint once, preserving author/template choices.
func migrateLinkPreferences(db *sql.DB) error {
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='preferences'`).Scan(&schema); err != nil {
		return err
	}
	if strings.Contains(schema, "BETWEEN 3 AND 7") {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE preferences_next (id INTEGER PRIMARY KEY CHECK(id=1),author TEXT NOT NULL,template TEXT NOT NULL,link_length INTEGER NOT NULL CHECK(link_length BETWEEN 3 AND 7));
 INSERT INTO preferences_next SELECT id,author,template,3 FROM preferences;
 DROP TABLE preferences;
 ALTER TABLE preferences_next RENAME TO preferences;`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
