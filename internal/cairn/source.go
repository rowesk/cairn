package cairn

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Source files keep the uploaded bytes. Direct routes sandbox HTML and SVG.
// Their names never determine a disk path.
// A large upload arrives as Path, a synced temporary file renamed into place.
type sourceFile struct {
	Name string
	Data []byte
	Path string
	Size int64
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
