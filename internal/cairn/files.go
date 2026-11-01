package cairn

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// File pages keep one uploaded file as the page itself: an icon, title and
// download button instead of rendered report HTML.
const maxFileBytes = 50 << 20

// Supporting images held in memory per request, and the whole request:
// field values, those images and multipart overhead on top of the file.
const maxUploadMemoryBytes = 16 << 20
const maxUploadRequestBytes = maxFileBytes + maxUploadMemoryBytes + 2<<20

var errUploadTooLarge = errors.New("upload too large")

type uploadedFile struct {
	Name string
	Data []byte // memory parts
	Path string // the streamed part, written under the data directory
	Size int64
}

type upload struct {
	fields map[string]string
	files  map[string][]uploadedFile
}

// remove deletes a streamed temporary file. After writeObject renames it
// into an object, removal is a harmless no-op.
func (u *upload) remove() {
	for _, files := range u.files {
		for _, f := range files {
			if f.Path != "" {
				os.Remove(f.Path)
			}
		}
	}
}

// readUpload streams a multipart body. The part named streamed goes to a
// synced temporary file in the data directory, so large files never sit in
// memory or in a RAM-backed /tmp. With a nonzero memoryLimit, up to 32
// supporting images named "assets" stay in memory, each within memoryLimit
// and together within 16 MiB. Text fields hold up to 64 KiB each.
func (s *Server) readUpload(r *http.Request, streamed string, memoryLimit int64) (*upload, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("send multipart/form-data")
	}
	u := &upload{fields: map[string]string{}, files: map[string][]uploadedFile{}}
	parts := 0
	var held int64
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return u, nil
		}
		if err != nil {
			u.remove()
			if isPayloadTooLarge(err) {
				return nil, errUploadTooLarge
			}
			return nil, fmt.Errorf("invalid multipart upload")
		}
		if parts++; parts > 64 {
			part.Close()
			u.remove()
			return nil, fmt.Errorf("too many form parts")
		}
		name := part.FormName()
		if part.FileName() == "" {
			value, err := io.ReadAll(io.LimitReader(part, (64<<10)+1))
			part.Close()
			if err != nil || len(value) > 64<<10 {
				u.remove()
				if isPayloadTooLarge(err) {
					return nil, errUploadTooLarge
				}
				return nil, fmt.Errorf("form field %s is too long", name)
			}
			if _, seen := u.fields[name]; !seen {
				u.fields[name] = string(value)
			}
			continue
		}
		// Older Windows browsers send a full C:\ path.
		original := part.FileName()
		original = original[strings.LastIndex(original, `\`)+1:]
		file := uploadedFile{Name: filepath.Base(original)}
		if name == streamed {
			if len(u.files[name]) > 0 {
				part.Close()
				u.remove()
				return nil, fmt.Errorf("send one file")
			}
			file.Path, file.Size, err = s.spool(part)
		} else if memoryLimit == 0 || name != "assets" || len(u.files[name]) >= 32 {
			part.Close()
			u.remove()
			return nil, fmt.Errorf("unexpected file part %s", name)
		} else {
			file.Data, err = io.ReadAll(io.LimitReader(part, memoryLimit+1))
			file.Size = int64(len(file.Data))
			held += file.Size
			if err == nil && (file.Size > memoryLimit || held > maxUploadMemoryBytes) {
				err = errUploadTooLarge
			}
		}
		part.Close()
		if err == nil || file.Path != "" {
			u.files[name] = append(u.files[name], file)
		}
		if err != nil {
			u.remove()
			if errors.Is(err, errUploadTooLarge) || isPayloadTooLarge(err) {
				return nil, errUploadTooLarge
			}
			return nil, fmt.Errorf("could not store the upload")
		}
	}
}

func (s *Server) spool(part io.Reader) (string, int64, error) {
	f, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "uploads"), "upload-")
	if err != nil {
		return "", 0, err
	}
	size, err := io.Copy(f, io.LimitReader(part, maxFileBytes+1))
	if err == nil && size > maxFileBytes {
		err = errUploadTooLarge
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return f.Name(), size, err
}

// filePublication turns a spooled upload into a publication whose saved HTML
// is a short description, used for library previews and the content route.
func filePublication(in *publication, f uploadedFile) error {
	if f.Size == 0 {
		return fmt.Errorf("the file is empty")
	}
	if f.Name == "" || f.Name == "." || len(f.Name) > 255 || !utf8.ValidString(f.Name) || strings.ContainsAny(f.Name, "\x00\r\n/\\") {
		return fmt.Errorf("the file needs a plain UTF-8 name of at most 255 bytes")
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		if strings.EqualFold(filepath.Ext(in.Title), ".tar") {
			in.Title = strings.TrimSuffix(in.Title, filepath.Ext(in.Title))
		}
		if strings.TrimSpace(in.Title) == "" {
			in.Title = f.Name
		}
	}
	in.File = true
	in.Source = &sourceFile{Name: f.Name, Path: f.Path, Size: f.Size}
	in.Markdown, in.Template, in.Capabilities, in.Assets = "", "", nil, nil
	return importBody(in, `<h1>`+template.HTMLEscapeString(in.Title)+`</h1><p>`+template.HTMLEscapeString(f.Name+" · "+fileTypeName(f.Name)+" · "+formatFileSize(f.Size))+`</p><p>Open this page in Cairn to download the file.</p>`)
}

// publishFile is the agent API for file pages: multipart/form-data with one
// file part named "file" and the page metadata as text fields.
func (s *Server) publishFile(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "none") {
		writeAPIError(w, http.StatusForbidden, ErrBrowserOriginForbidden, "browser publication is not permitted", false)
		return
	}
	author, authenticated := s.publisherIdentity(r)
	if !authenticated {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cairn"`)
		writeAPIError(w, http.StatusUnauthorized, ErrAuthenticationRequired, "publisher credential required", false)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		writeAPIError(w, http.StatusUnsupportedMediaType, ErrUnsupportedMediaType, "multipart/form-data required", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	u, err := s.readUpload(r, "file", 0)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, ErrPayloadTooLarge, "the file must fit 50 MiB", false)
		} else {
			writeAPIError(w, http.StatusBadRequest, ErrValidationFailed, err.Error(), false)
		}
		return
	}
	defer u.remove()
	for name := range u.fields {
		switch name {
		case "title", "agent", "original_request", "request_summary", "request_source", "slug":
		default:
			writeAPIErrorField(w, http.StatusBadRequest, ErrValidationFailed, name, "unknown field "+name, false)
			return
		}
	}
	for name := range u.files {
		if name != "file" {
			writeAPIErrorField(w, http.StatusBadRequest, ErrValidationFailed, name, "send exactly one file part named file", false)
			return
		}
	}
	if len(u.files["file"]) != 1 {
		writeAPIErrorField(w, http.StatusBadRequest, ErrValidationFailed, "file", "send exactly one file part named file", false)
		return
	}
	in := publication{Title: u.fields["title"], Agent: u.fields["agent"], OriginalRequest: u.fields["original_request"], RequestSummary: u.fields["request_summary"], RequestSource: u.fields["request_source"], Slug: u.fields["slug"]}
	if err := filePublication(&in, u.files["file"][0]); err != nil {
		writeAPIErrorField(w, http.StatusBadRequest, ErrValidationFailed, "file", err.Error(), false)
		return
	}
	if author != "" {
		in.Agent = author
		r = r.WithContext(context.WithValue(r.Context(), publisherAuthorKey{}, author))
	}
	s.savePublication(w, r, in)
}

// serveSource opens the stored file under the read lock, then releases the
// lock before sending it. A slow 50 MiB download must not hold up saves.
// The open descriptor stays valid if the page is replaced or deleted meanwhile.
func (s *Server) serveSource(w http.ResponseWriter, r *http.Request, p savedPage, unlock func()) {
	if p.FileName == "" {
		unlock()
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.cfg.DataDir, "objects", p.Object, "source.bin"))
	unlock()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": p.FileName}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, "", info.ModTime(), f)
}
