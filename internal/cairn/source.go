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
func (s *Server) downloadSource(w http.ResponseWriter, r *http.Request, p savedPage) {
	if p.FileName == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": p.FileName}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeFile(w, r, filepath.Join(s.cfg.DataDir, "objects", p.Object, "source.bin"))
}
