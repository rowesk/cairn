package cairn

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type storageFile struct {
	Slug, Name string
	Size       int64
}

type storageUsage struct {
	Total, Pages, Files, Other int64
	Free, Capacity             int64
	DiskKnown, Incomplete      bool
	Largest                    []storageFile
}

// measureStorage counts stored bytes, not filesystem allocation blocks. File
// payloads use the catalogue's file_size; their wrappers count as everything
// else. Archived items still occupy storage and are included.
func (s *Server) measureStorage(parent context.Context) storageUsage {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	var usage storageUsage
	usage.Free, usage.Capacity, usage.DiskKnown = storageDiskSpace(s.cfg.DataDir)
	pageObjects := make(map[string]bool)
	rows, err := s.db.QueryContext(ctx, `SELECT object,kind,file_size FROM pages`)
	if err != nil {
		usage.Incomplete = true
	} else {
		for rows.Next() {
			var object, kind string
			var size int64
			if err := rows.Scan(&object, &kind, &size); err != nil {
				usage.Incomplete = true
				break
			}
			if kind == "page" {
				pageObjects[object] = true
			}
			if kind == "file" {
				usage.Files += size
			}
		}
		if rows.Err() != nil {
			usage.Incomplete = true
		}
		rows.Close()
	}
	rows, err = s.db.QueryContext(ctx, `SELECT slug,COALESCE(NULLIF(file_name,''),title),file_size FROM pages WHERE kind='file' ORDER BY file_size DESC,slug LIMIT 10`)
	if err != nil {
		usage.Incomplete = true
	} else {
		for rows.Next() {
			var file storageFile
			if err := rows.Scan(&file.Slug, &file.Name, &file.Size); err != nil {
				usage.Incomplete = true
				break
			}
			usage.Largest = append(usage.Largest, file)
		}
		if rows.Err() != nil {
			usage.Incomplete = true
		}
		rows.Close()
	}
	for _, name := range []string{"cairn.sqlite", "cairn.sqlite-wal", "cairn.sqlite-shm", "objects", "uploads"} {
		root := filepath.Join(s.cfg.DataDir, name)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				// WAL and temporary uploads may disappear while measuring.
				if !errors.Is(walkErr, os.ErrNotExist) || name == "cairn.sqlite" || name == "objects" {
					usage.Incomplete = true
				}
				return nil
			}
			// Never follow a symlink out of Cairn's data directory.
			if !entry.Type().IsRegular() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				usage.Incomplete = true
				return nil
			}
			usage.Total += info.Size()
			if name == "objects" {
				relative, err := filepath.Rel(root, path)
				if err == nil {
					object, _, _ := strings.Cut(relative, string(filepath.Separator))
					if pageObjects[object] {
						usage.Pages += info.Size()
					}
				}
			}
			return nil
		})
		if err != nil {
			usage.Incomplete = true
			break
		}
	}
	// A concurrent upload, deletion or partial walk can leave the catalogue
	// ahead of the measured bytes. Do not display a negative remainder.
	if usage.Total < usage.Pages+usage.Files {
		usage.Incomplete = true
	}
	usage.Other = max(0, usage.Total-usage.Pages-usage.Files)
	return usage
}
