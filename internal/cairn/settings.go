package cairn

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
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
	"unicode/utf8"
)

type preferences struct {
	Author, Template string
	LinkLength       int
}
type authorProfile struct {
	Name                string
	Photo               template.URL
	KeyActive           bool
	KeyCreated, KeyUsed string
}
type settingsData struct {
	CSRF, Error, Message string
	Preferences          preferences
	Profiles             []authorProfile
}

//go:embed ui/settings.html
var settingsHTML string

//go:embed contract.md
var agentSetupGuide string

// ui/agent-setup.md is a rendered copy of contract.md for repo readers.
// The Settings UI and agent setup text always serve contract.md above.

//go:embed ui/settings.js
var settingsBodyJS string
var settingsJS = focusJS + "\n" + settingsBodyJS
var settingsHash = func() string {
	sum := sha256.Sum256([]byte(settingsJS))
	return base64.StdEncoding.EncodeToString(sum[:])
}()
var settingsView = template.Must(template.New("settings").Funcs(uiFunctions).Funcs(template.FuncMap{"setupGuide": func() string { return agentSetupGuide }, "settingsScript": func() template.JS { return template.JS(settingsJS) }}).Parse(settingsHTML))

func (s *Server) preferences() preferences {
	p := preferences{Author: "Owner", Template: "report", LinkLength: 3}
	_ = s.db.QueryRow(`SELECT author,template,link_length FROM preferences WHERE id=1`).Scan(&p.Author, &p.Template, &p.LinkLength)
	return p
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
	for rows.Next() {
		var name string
		var photo []byte
		var active bool
		var created, used string
		if err := rows.Scan(&name, &photo, &active, &created, &used); err != nil {
			return nil, err
		}
		profiles = append(profiles, authorProfile{Name: name, Photo: photoURL(photo), KeyActive: active, KeyCreated: created, KeyUsed: used})
	}
	return profiles, rows.Err()
}
func normalizedPhoto(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return nil, fmt.Errorf("Use a PNG, JPEG or GIF photo up to 4,096 pixels per side and 16 megapixels.")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("That photo could not be read. Try a PNG or JPEG.")
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
	privateFormHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src data:; img-src data: blob:; connect-src 'self'; script-src 'sha256-"+settingsHash+"'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	d := settingsData{CSRF: s.csrf, Preferences: s.preferences()}
	status := 200
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
		if err != nil {
			d.Error = "The upload is too large or unreadable. Use a photo under 4 MiB."
			status = 400
		} else if !s.ownerRequest(r) {
			http.Error(w, "reload settings and try again", 403)
			return
		} else {
			switch r.FormValue("action") {
			case "key_create", "key_rotate", "key_revoke":
				s.changeAgentKey(w, r)
				return
			case "defaults":
				length, e := strconv.Atoi(r.FormValue("link_length"))
				author := strings.TrimSpace(r.FormValue("author"))
				kind := r.FormValue("template")
				if e != nil || length < 3 || length > 7 || len(author) == 0 || len(author) > 100 || !utf8.ValidString(author) || (kind != "report" && kind != "comparison" && kind != "visual") {
					d.Error = "Use an author name up to 100 bytes, a listed template and a link length from 3 to 7."
					status = 400
					break
				}
				_, err = s.db.Exec(`UPDATE preferences SET author=?,template=?,link_length=? WHERE id=1`, author, kind, length)
			case "photo", "remove_photo":
				name := strings.TrimSpace(r.FormValue("name"))
				if name == "" || len(name) > 100 || !utf8.ValidString(name) {
					d.Error = "Enter the author name exactly as it appears on their reports."
					status = 400
					break
				}
				if r.FormValue("action") == "remove_photo" {
					_, err = s.db.Exec(`DELETE FROM author_profiles WHERE name=?`, name)
					break
				}
				file, _, e := r.FormFile("photo")
				if e != nil {
					d.Error = "Choose a photo first."
					status = 400
					break
				}
				data, e := io.ReadAll(io.LimitReader(file, (4<<20)+1))
				file.Close()
				if e != nil || len(data) > 4<<20 {
					d.Error = "Use a photo under 4 MiB."
					status = 400
					break
				}
				data, e = normalizedPhoto(data)
				if e != nil {
					d.Error = e.Error()
					status = 400
					break
				}
				_, err = s.db.Exec(`INSERT INTO author_profiles(name,photo) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET photo=excluded.photo`, name, data)
			default:
				d.Error = "Choose a settings action."
				status = 400
			}
			if err != nil {
				d.Error = "Settings could not be saved. Try again."
				status = 500
			}
			if status == 200 {
				http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
				return
			}
		}
	} else if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Query().Get("saved") == "1" {
		d.Message = "Saved."
	}
	var err error
	d.Profiles, err = s.profiles()
	if err != nil && err != sql.ErrNoRows {
		http.Error(w, "settings unavailable", 500)
		return
	}
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
