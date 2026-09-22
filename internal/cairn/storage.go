package cairn

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Write and sync a complete object before SQLite atomically points at it.
// Unreferenced objects are temporary, never readable through HTTP.
func (s *Server) writeObject(in publication) (object string, err error) {
	object = randomID()
	root := filepath.Join(s.cfg.DataDir, "objects")
	dir := filepath.Join(root, object)
	if err = os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			s.removeObject(filepath.Base(dir))
		}
	}()
	if err = writeSynced(filepath.Join(dir, "page.html"), []byte(in.HTML)); err != nil {
		return "", err
	}
	if in.Source != nil {
		metadata, e := json.Marshal(struct{ Name string }{in.Source.Name})
		if e != nil {
			return "", e
		}
		if err = writeSynced(filepath.Join(dir, "source.json"), metadata); err != nil {
			return "", err
		}
		if err = writeSynced(filepath.Join(dir, "source.bin"), in.Source.Data); err != nil {
			return "", err
		}
	}
	if len(in.Assets) > 0 {
		assets := filepath.Join(dir, "assets")
		if err = os.Mkdir(assets, 0700); err != nil {
			return "", err
		}
		for name, data := range in.Assets {
			if err = writeSynced(filepath.Join(assets, name), data); err != nil {
				return "", err
			}
		}
		if err = syncDir(assets); err != nil {
			return "", err
		}
	}
	if err = syncDir(dir); err != nil {
		return "", err
	}
	if err = syncDir(root); err != nil {
		return "", err
	}
	return object, nil
}
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Called under the exclusive data-directory process lock after SQLite recovery.
func (s *Server) removeUnreferencedObjects() error {
	rows, err := s.db.Query(`SELECT object FROM pages`)
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		live[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	root := filepath.Join(s.cfg.DataDir, "objects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !live[entry.Name()] {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
