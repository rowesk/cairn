package cairn

import (
	"strings"
	"testing"
)

func TestFileCategory(t *testing.T) {
	for name, want := range map[string]string{
		"Q3.ZIP": "archive", "backup.tar.gz": "archive", "budget.xlsx": "spreadsheet", "rows.csv": "spreadsheet",
		"notes.docx": "document", "README.md": "document", "deck.key": "presentation", "photo.HEIC": "image",
		"cover.psd": "design", "brand.afdesign": "design", "song.flac": "audio", "clip.mov": "video",
		"main.go": "code", "config.yaml": "data", "manual.pdf": "pdf", "mystery.xyz": "other", "noext": "other",
		"trailing.": "other", "": "other",
	} {
		if got := fileCategory(name); got != want {
			t.Errorf("fileCategory(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFileLabel(t *testing.T) {
	for name, want := range map[string]string{
		"archive.tar.gz": "GZ", "budget.xlsx": "XLSX", "brand.afdesign": "AFDE", "noext": "FILE", "trailing.": "FILE",
		"x.<b>": "B", "x.é-1": "1", "x.\"><": "FILE", "x.7z": "7Z", ".bashrc": "BASH", "dir.v2/file": "FILE",
	} {
		if got := fileLabel(name); got != want {
			t.Errorf("fileLabel(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 bytes", 1: "1 byte", 512: "512 bytes", 1023: "1023 bytes", 1024: "1 KB", 1536: "1.5 KB",
		10239: "10 KB", 10240: "10 KB", 14 << 10: "14 KB", 1048575: "1 MB", 1 << 20: "1 MB",
		3355443: "3.2 MB", 50 << 20: "50 MB", 5 << 30: "5 GB", 3 << 40: "3 TB", 2048 << 40: "2048 TB",
	} {
		if got := formatFileSize(n); got != want {
			t.Errorf("formatFileSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFileTypeName(t *testing.T) {
	for name, want := range map[string]string{
		"a.zip": "ZIP archive", "a.XLSX": "Excel spreadsheet", "a.psd": "Photoshop document", "a.png": "PNG image",
		"a.pdf": "PDF document", "a.xyz": "XYZ file", "a.longext": "LONG file", "noext": "File", "a.<>": "File",
	} {
		if got := fileTypeName(name); got != want {
			t.Errorf("fileTypeName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFileIcon(t *testing.T) {
	for name, class := range map[string]string{"a.zip": "file-icon-archive", "a.xyz": "file-icon-other", "a.pdf": "file-icon-pdf"} {
		got := string(fileIcon(name))
		if !strings.Contains(got, `class="file-icon `+class+`"`) || !strings.Contains(got, `aria-hidden="true"`) || !strings.Contains(got, `class="file-icon-label"`) {
			t.Errorf("fileIcon(%q) missing classes: %s", name, got)
		}
		if strings.Contains(got, "fill=") {
			t.Errorf("fileIcon(%q) hard-codes a fill", name)
		}
	}
	got := string(fileIcon(`x."><script>.<img`))
	if strings.Contains(got, "<script") || strings.Contains(got, "<img") || !strings.Contains(got, ">IMG</text>") {
		t.Errorf("fileIcon did not sanitise label: %s", got)
	}
	for _, category := range []string{"archive", "spreadsheet", "document", "presentation", "image", "design", "audio", "video", "code", "data", "pdf", "other"} {
		if fileGlyphs[category] == "" {
			t.Errorf("no glyph for %s", category)
		}
	}
}
