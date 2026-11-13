package cairn

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// shortTargetProblem never fetches the target. Local public paths are refused
// even before a matching link exists, so later links cannot create a loop.
func (s *Server) shortTargetProblem(target string) string {
	if !utf8.ValidString(target) || utf8.RuneCountInString(target) > 2048 {
		return "Use a URL of at most 2,048 characters."
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || strings.ContainsAny(target, "\r\n\t ") {
		return "Enter an http or https URL with a valid host."
	}
	if !ValidHostName(u.Hostname()) {
		return "Enter an http or https URL with a valid host."
	}
	if u.Port() != "" {
		if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
			return "Enter a valid host and port."
		}
	}
	public, _ := url.Parse(s.cfg.PublicBaseURL)
	if public != nil && strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), strings.TrimSuffix(public.Hostname(), ".")) && validSlug(strings.Trim(u.Path, "/")) {
		return "Choose a target outside this server's public link paths."
	}
	return ""
}

// Caller holds the read lock until this returns. Shutdown waits for writes
// already scheduled, but a visitor never waits for the click write.
func (s *Server) redirectShortLink(w http.ResponseWriter, r *http.Request, p savedPage) {
	if r.Method == http.MethodGet {
		s.clickWrites.Add(1)
		go func() {
			defer s.clickWrites.Done()
			if _, err := s.db.Exec(`UPDATE pages SET clicks=clicks+1 WHERE id=?`, p.ID); err != nil {
				s.log.Error("count short link click", "error", err)
			}
		}()
	}
	w.Header().Set("Location", p.TargetURL)
	w.WriteHeader(http.StatusFound)
}

func (s *Server) apiShortLinks(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "none") {
		writeAPIError(w, 403, ErrBrowserOriginForbidden, "Browsers cannot use the API.", false)
		return
	}
	cred, failure := s.authenticate(r)
	if failure != "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cairn"`)
		writeAPIError(w, 401, ErrAuthenticationRequired, "Send a valid agent key in an Authorization: Bearer header.", false)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeAPIError(w, 405, ErrMethodNotAllowed, "Use POST to create a link.", false)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeAPIError(w, 415, ErrUnsupportedMediaType, "Send JSON with Content-Type: application/json.", false)
		return
	}
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if err := dec.Decode(&raw); err != nil || raw == nil {
		writeAPIError(w, 400, ErrInvalidJSON, "Send one JSON object.", false)
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeAPIError(w, 400, ErrInvalidJSON, "Send one JSON object.", false)
		return
	}
	var target, slug string
	shared := false
	problems := []FieldError{}
	for key, value := range raw {
		var err error
		switch key {
		case "url":
			err = json.Unmarshal(value, &target)
		case "slug":
			err = json.Unmarshal(value, &slug)
		case "share_enabled":
			err = json.Unmarshal(value, &shared)
		default:
			problems = append(problems, FieldError{Code: FieldNotAllowed, Field: key, Message: "This field is not available for links. Links have no password."})
		}
		if err != nil || string(value) == "null" {
			problems = append(problems, FieldError{Code: FieldWrongType, Field: key, Message: "Use the documented field type."})
		}
	}
	if message := s.shortTargetProblem(target); message != "" {
		problems = append(problems, FieldError{Code: FieldInvalidValue, Field: "url", Message: message})
	}
	if len(problems) > 0 {
		writeAPIProblems(w, 400, ErrValidationFailed, problems)
		return
	}
	author := cred.Agent
	if author == "" {
		author = s.preferences().Author
	}
	in := publication{Slug: slug, Title: shortHost(target), Agent: author, TargetURL: target, Share: shared}
	s.savePublication(w, r.WithContext(context.WithValue(r.Context(), publishContextKey{}, &publishContext{cred: cred})), in)
}
func shortHost(target string) string {
	u, _ := url.Parse(target)
	if u == nil {
		return ""
	}
	return u.Host
}

type shortNewData struct {
	Notice *notice
	ownerChrome
	Theme        string
	CSRF         formToken
	Target, Slug string
	Errors       fieldErrors
}

var shortNewView = pageView("short-new", shortNewHTML, nil)
var shortManageView = pageView("short-manage", shortManageHTML, nil)

func (s *Server) newShortLink(w http.ResponseWriter, r *http.Request) {
	d := shortNewData{ownerChrome: s.chrome(), Theme: s.themes().Admin, CSRF: s.formToken(), Errors: fieldErrors{}}
	status := 200
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		err := r.ParseForm()
		if !sameOrigin(r) || !s.freshToken(r) {
			s.showError(w, 403, problemForeignForm)
			return
		}
		if err != nil {
			s.showError(w, 400, problemUnreadable("/new-link"))
			return
		}
		d.Target = strings.TrimSpace(r.FormValue("url"))
		d.Slug = strings.TrimSpace(r.FormValue("slug"))
		if message := s.shortTargetProblem(d.Target); message != "" {
			d.Errors["url"] = message
		}
		if d.Slug != "" {
			if p := slugProblem(d.Slug); p != nil {
				d.Errors["slug"] = p.Message
			}
		}
		if len(d.Errors) == 0 {
			result := httptest.NewRecorder()
			s.savePublication(result, r, publication{Slug: d.Slug, Title: shortHost(d.Target), Agent: s.preferences().Author, TargetURL: d.Target, OwnerWritten: true})
			if result.Code == http.StatusCreated {
				var out apiPageWrite
				_ = json.Unmarshal(result.Body.Bytes(), &out)
				http.Redirect(w, r, "/"+out.Slug, http.StatusSeeOther)
				return
			}
			d.Errors["slug"] = "Cairn could not save this link. Check the link name and try again."
		}
		status = 400
	} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.methodNotAllowed(w, "GET, HEAD, POST")
		return
	}
	formPageHeaders(w)
	w.WriteHeader(status)
	_ = shortNewView.Execute(w, d)
}

//go:embed ui/short-new.html
var shortNewHTML string

//go:embed ui/short-manage.html
var shortManageHTML string
