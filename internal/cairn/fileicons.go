package cairn

import (
	"fmt"
	"html/template"
	"path/filepath"
	"strings"
)

var fileCategories = func() map[string]string {
	groups := map[string]string{
		"archive":      "zip 7z rar tar gz tgz bz2 xz zst dmg iso",
		"spreadsheet":  "xlsx xls xlsm ods csv tsv numbers",
		"document":     "doc docx odt rtf txt md pages epub",
		"presentation": "ppt pptx odp key",
		"image":        "png jpg jpeg gif webp heic heif tif tiff bmp svg avif raw cr2 nef dng",
		"design":       "psd psb ai sketch fig xd indd afdesign afphoto eps",
		"audio":        "mp3 wav m4a aac flac ogg aiff",
		"video":        "mp4 mov m4v mkv webm avi",
		"code":         "go js ts py rb php html css sh swift java c h cpp rs sql",
		"data":         "json xml yaml yml toml sqlite db log",
		"pdf":          "pdf",
	}
	m := map[string]string{}
	for category, exts := range groups {
		for _, ext := range strings.Fields(exts) {
			m[ext] = category
		}
	}
	return m
}()

var fileTypeNames = map[string]string{
	"zip": "ZIP archive", "7z": "7-Zip archive", "rar": "RAR archive", "tar": "Tar archive",
	"gz": "Gzip archive", "tgz": "Gzip archive", "dmg": "Disk image", "iso": "Disk image",
	"xlsx": "Excel spreadsheet", "xls": "Excel spreadsheet", "xlsm": "Excel spreadsheet",
	"ods": "OpenDocument spreadsheet", "numbers": "Numbers spreadsheet", "csv": "CSV file", "tsv": "TSV file",
	"doc": "Word document", "docx": "Word document", "odt": "OpenDocument text", "rtf": "Rich text document",
	"txt": "Text file", "md": "Markdown file", "pages": "Pages document", "epub": "EPUB book",
	"ppt": "PowerPoint presentation", "pptx": "PowerPoint presentation", "key": "Keynote presentation", "odp": "OpenDocument presentation",
	"png": "PNG image", "jpg": "JPEG image", "jpeg": "JPEG image", "gif": "GIF image", "webp": "WebP image",
	"heic": "HEIC image", "heif": "HEIF image", "tif": "TIFF image", "tiff": "TIFF image", "svg": "SVG image",
	"avif": "AVIF image", "raw": "RAW photo", "cr2": "RAW photo", "nef": "RAW photo", "dng": "RAW photo",
	"psd": "Photoshop document", "psb": "Photoshop document", "ai": "Illustrator document", "sketch": "Sketch file",
	"fig": "Figma file", "xd": "Adobe XD file", "indd": "InDesign document", "afdesign": "Affinity Designer file",
	"afphoto": "Affinity Photo file", "eps": "EPS graphic",
	"mp3": "MP3 audio", "wav": "WAV audio", "m4a": "M4A audio", "flac": "FLAC audio",
	"mp4": "MP4 video", "mov": "QuickTime video", "mkv": "MKV video", "webm": "WebM video",
	"json": "JSON file", "xml": "XML file", "yaml": "YAML file", "yml": "YAML file", "sqlite": "SQLite database", "db": "Database",
	"pdf": "PDF document",
}

var fileGlyphs = map[string]string{
	"archive":      `<path d="M24 4v24M22 8h2M24 11h2M22 14h2M24 17h2M22 20h2M24 23h2"/><rect x="21" y="27" width="6" height="7" rx="1"/>`,
	"spreadsheet":  `<rect x="14" y="15" width="20" height="18" rx="1"/><path d="M14 21h20M14 27h20M21 15v18"/>`,
	"document":     `<path d="M15 17h18M15 22h18M15 27h18M15 32h11"/>`,
	"presentation": `<rect x="13" y="15" width="22" height="14" rx="1"/><path d="M24 29v4M20 33h8"/>`,
	"image":        `<rect x="14" y="15" width="20" height="18" rx="2"/><circle cx="29" cy="20" r="2"/><path d="m15 31 6-7 5 5 3-3 4 4"/>`,
	"design":       `<path d="m24 14 7 9-7 11-7-11zM24 14v10"/><circle cx="24" cy="26" r="1.5"/>`,
	"audio":        `<path d="M21.5 31V17l10-2v13"/><circle cx="19" cy="31" r="2.5"/><circle cx="29" cy="28" r="2.5"/>`,
	"video":        `<rect x="14" y="16" width="20" height="16" rx="2"/><path d="M22 20v8l6-4z"/>`,
	"code":         `<path d="m19 19-5 5 5 5M29 19l5 5-5 5M26 17l-4 14"/>`,
	"data":         `<path d="M20 16c-3 0-3 2-3 4s0 3-2 4c2 1 2 2 2 4s0 4 3 4M28 16c3 0 3 2 3 4s0 3 2 4c-2 1-2 2-2 4s0 4-3 4"/>`,
	"pdf":          `<path d="M17 15h14v18l-7-5-7 5z"/>`,
	"other":        `<path d="M17 20h14M17 25h14M17 30h9"/>`,
}

func fileExt(name string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
}

func fileCategory(name string) string {
	if category, ok := fileCategories[fileExt(name)]; ok {
		return category
	}
	return "other"
}

func fileLabel(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(fileExt(name)) {
		if (r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') && b.Len() < 4 {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "FILE"
	}
	return b.String()
}

func fileTypeName(name string) string {
	if kind, ok := fileTypeNames[fileExt(name)]; ok {
		return kind
	}
	if label := fileLabel(name); label != "FILE" {
		return label + " file"
	}
	return "File"
}

func fileIcon(name string) template.HTML {
	category := fileCategory(name)
	return template.HTML(`<svg class="file-icon file-icon-` + category + `" viewBox="0 0 48 56" aria-hidden="true">` +
		`<path class="file-icon-sheet" d="M8 2h24l12 12v38a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2z"/>` +
		`<path class="file-icon-fold" d="M32 2v10a2 2 0 0 0 2 2h10"/>` +
		`<g class="file-icon-glyph" transform="translate(1 0)">` + fileGlyphs[category] + `</g>` +
		`<rect class="file-icon-band" x="6" y="39" width="38" height="13"/>` +
		`<text class="file-icon-label" x="25" y="48.5" text-anchor="middle">` + template.HTMLEscapeString(fileLabel(name)) + `</text></svg>`)
}

func formatFileSize(n int64) string {
	if n == 1 {
		return "1 byte"
	}
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	v, unit := float64(n)/1024, 0
	units := []string{"KB", "MB", "GB", "TB"}
	for v >= 1023.5 && unit < len(units)-1 {
		v, unit = v/1024, unit+1
	}
	s := fmt.Sprintf("%.0f", v)
	if v < 9.95 {
		s = strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
	}
	return s + " " + units[unit]
}
