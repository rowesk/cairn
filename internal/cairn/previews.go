package cairn

import (
	_ "embed"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed ui/preview.png
var previewCard []byte

var previewCardPath = "/_ui/" + hashedName("preview.png", previewCard)

func init() {
	uiAssets[strings.TrimPrefix(previewCardPath, "/_ui/")] = &uiAsset{body: previewCard, contentType: "image/png"}
}

type linkPreview struct {
	Title, Description, URL, Image, Video, VideoType string
	Width, Height                                    int
}

// Only readers and password forms can supply public preview metadata.
func publicPreview(data any) *linkPreview {
	switch d := data.(type) {
	case readerData:
		return d.Social
	case gateData:
		return d.Social
	}
	return nil
}

// Protected metadata is deliberately built before using the excerpt or reading an image.
// The stored preview comes from the page body, never original_request or its summary.
func (s *Server) linkPreview(p savedPage, protected bool) *linkPreview {
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	if base == "" {
		return nil
	}
	var slug, excerpt string
	if err := s.db.QueryRow(`SELECT coalesce(nullif(public_slug,''),slug),preview FROM pages WHERE id=? AND share_enabled=1`, p.ID).Scan(&slug, &excerpt); err != nil {
		return nil
	}
	d := &linkPreview{URL: base + "/" + slug, Image: base + previewCardPath, Width: 1200, Height: 630}
	if protected {
		d.Title = "Protected page"
		if p.File {
			d.Title = "Protected file"
		}
		return d
	}
	d.Title = p.Title
	text := []rune(strings.Join(strings.Fields(excerpt), " "))
	if len(text) > 200 {
		text = text[:200]
	}
	d.Description = string(text)
	if ext := directExtension(p); ext != "" {
		direct := d.URL + ext
		switch filePreview(p.FileName) {
		case "image":
			d.Image, d.Width, d.Height = direct, 0, 0
			// DecodeConfig reads only the header, bounded even for malformed uploads.
			if f, err := os.Open(filepath.Join(s.cfg.DataDir, "objects", p.Object, "source.bin")); err == nil {
				if cfg, _, err := image.DecodeConfig(io.LimitReader(f, 1<<20)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
					d.Width, d.Height = cfg.Width, cfg.Height
				}
				f.Close()
			}
		case "video":
			d.Video, d.VideoType = direct, directType(p.FileName)
		}
	}
	return d
}
