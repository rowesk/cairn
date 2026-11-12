package cairn

import (
	"encoding/json"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// Source files are inert attachments. Their names never determine a disk path.
type sourceFile struct {
	Name string
	Data []byte
}

func (s *Server) sourceName(p savedPage) string {
	data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "source.json"))
	if err != nil {
		return ""
	}
	var meta struct{ Name string }
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return meta.Name
}

// downloadSource sends a page's original file. missing is the listener's
// own 404, so the owner and visitors each get their themed page.
func (s *Server) downloadSource(w http.ResponseWriter, r *http.Request, p savedPage, missing http.HandlerFunc) {
	path := filepath.Join(s.cfg.DataDir, "objects", p.Object, "source.bin")
	if info, err := os.Stat(path); p.FileName == "" || err != nil || info.IsDir() {
		missing(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": p.FileName}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeFile(w, r, path)
}
