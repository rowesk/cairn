package cairn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Feedback: the themed error page, the notice component, inline field
// errors and the owner form token. Browser routes never answer with a bare
// plain-text body. The JSON API under /api keeps its own shape (errors.go).

// notice is one message placed beside what it concerns. Kind is "success",
// "error" or "info". Each kind has its own icon and wording, so colour is
// never the only signal. Link offers a next step; Retry repeats a form post.
// Render it with {{template "notice" .}} from ui/feedback.html.
type notice struct {
	Kind, Text     string
	Link, LinkText string
	Retry          *noticeRetry
}

// noticeRetry is a one-button form inside a notice, such as Try again.
type noticeRetry struct {
	Action, Label string
	Fields        map[string]string
}

func succeeded(text string) *notice { return &notice{Kind: "success", Text: text} }
func failed(text string) *notice    { return &notice{Kind: "error", Text: text} }
func informed(text string) *notice  { return &notice{Kind: "info", Text: text} }

// fieldErrors maps a form field's name to the message shown beside it.
type fieldErrors map[string]string

// expiredForm is the notice a form shows when its token no longer matches,
// for example after the form key was replaced. Nothing was saved.
const expiredForm = "This form had expired, so nothing was saved. Check it and submit again."

// problem is what the error page says: a plain title, one sentence on what
// happened, and at most one way on. Action and Href may be empty.
type problem struct {
	Title, Message string
	Action, Href   string
}

type errorData struct {
	ownerChrome
	Theme          string
	Visitor        bool
	Title, Message string
	Action, Href   string
	// Notice is always nil: error pages say everything in their body. The
	// shared layout reads it for the toast region.
	Notice *notice
}

//go:embed ui/error.html
var errorHTML string

//go:embed ui/feedback.html
var feedbackHTML string

var errorView = pageView("error", errorHTML, nil)

// showError renders the owner error page on the private listener: the top
// bar with its way back to the library, a title, one sentence and one action.
// Use it for any browser route that cannot show its own form again.
func (s *Server) showError(w http.ResponseWriter, status int, p problem) {
	s.renderProblem(w, status, p, false)
}

// showVisitorError renders the visitor variant for the public listener. It
// uses the public theme and the visitor shell, which shows the brand as text
// and nothing private. Its problem must never link into the library or name
// a private page, request or setting.
func (s *Server) showVisitorError(w http.ResponseWriter, status int, p problem) {
	s.renderProblem(w, status, p, true)
}

func (s *Server) renderProblem(w http.ResponseWriter, status int, p problem, visitor bool) {
	themes := s.themes()
	d := errorData{Theme: themes.Admin, Visitor: visitor, Title: p.Title, Message: p.Message, Action: p.Action, Href: p.Href}
	if visitor {
		d.Theme = themes.public()
	} else {
		d.ownerChrome = s.chrome()
	}
	h := w.Header()
	h.Del("Content-Disposition")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// frame-ancestors 'self' lets a report frame show why its content is gone.
	h.Set("Content-Security-Policy", "default-src 'none'; "+uiAssetPolicy+"; form-action 'none'; frame-ancestors 'self'; base-uri 'none'")
	w.WriteHeader(status)
	_ = errorView.Execute(w, d)
}

// The owner's problems.
var (
	problemMissingPage = problem{Title: "This page isn’t in your library", Message: "It may have been deleted, or the address may have a typo.", Action: "Back to library", Href: "/"}
	problemWrongMethod = problem{Title: "Cairn can’t do that here", Message: "This address only opens pages. Start again from your library.", Action: "Back to library", Href: "/"}
	problemForeignForm = problem{Title: "Cairn didn’t accept that request", Message: "It didn’t come from one of your Cairn pages. If you sent it, reload the page and try again.", Action: "Back to library", Href: "/"}
	problemNoMail      = problem{Title: "Send to Kindle isn’t set up", Message: "This server has no mail account to send from. Add one to the server’s settings, then restart Cairn.", Action: "Read how to add one", Href: kindleGuide}
)

func problemUnreadable(back string) problem {
	return problem{Title: "Cairn couldn’t read that form", Message: "It may have been too large or cut off on the way. Go back and try again.", Action: "Go back", Href: back}
}

func problemStorage(retry string) problem {
	return problem{Title: "Cairn couldn’t open your library", Message: "Its storage didn’t answer. Try again in a moment, and if it keeps happening, check that Cairn can read its data folder.", Action: "Try again", Href: retry}
}

func problemNotSaved(back string) problem {
	return problem{Title: "Cairn couldn’t save that change", Message: "Its storage didn’t accept the change, so nothing changed. Try again in a moment.", Action: "Back to the page", Href: back}
}

// The visitor's problems. None of them may mention the library.
var (
	problemBrokenLink = problem{Title: "This link isn’t working", Message: "The page may no longer be shared, or the address may have a typo. Ask the person who sent it for a new link."}
	problemInsecure   = problem{Title: "This link needs a secure connection", Message: "Open it with an address that starts with https:// to enter the password."}
)

func problemLocked(route string) problem {
	return problem{Title: "This page is protected by a password", Message: "Open the page and enter its password to read it.", Action: "Open the page", Href: "/" + route}
}

func problemForeignUnlock(route string) problem {
	return problem{Title: "Enter the password on the page itself", Message: "This request didn’t come from the page’s password form. Open the page and enter the password there.", Action: "Open the page", Href: "/" + route}
}

// notFound is the owner's 404 for every private browser route.
func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	s.showError(w, http.StatusNotFound, problemMissingPage)
}

// publicNotFound is the visitor's 404. It reveals nothing about whether a
// private page exists at the address.
func (s *Server) publicNotFound(w http.ResponseWriter, _ *http.Request) {
	s.showVisitorError(w, http.StatusNotFound, problemBrokenLink)
}

// methodNotAllowed answers a browser route that only reads.
func (s *Server) methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	s.showError(w, http.StatusMethodNotAllowed, problemWrongMethod)
}

// serveObjectFile serves one stored file, or the listener's own 404 when the
// file is missing, so a broken image link never shows a bare text body.
func serveObjectFile(w http.ResponseWriter, r *http.Request, path string, missing http.HandlerFunc) {
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		missing(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

// Owner form token. Every owner form carries it, and every owner post must
// also come from Cairn's own origin. The key behind it lives in the data
// directory beside share.key, so a restart no longer expires open forms.

func (s *Server) loadFormKey() error {
	path := filepath.Join(s.cfg.DataDir, "form.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = []byte(randomID() + randomID())
		if err = writeSynced(path, key); err != nil {
			return err
		}
		if err = syncDir(s.cfg.DataDir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if len(key) != 64 {
		return fmt.Errorf("%s is damaged; delete it and start Cairn again, then reload any open Cairn forms", path)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("cairn owner form token"))
	s.csrf = mac.Sum(nil)
	return nil
}

// formToken is the owner form token as a page shows it. Every time a page
// prints it, it is masked afresh: a random pad followed by the token XOR
// that pad, in hex. The secret therefore never appears the same way twice,
// and a compressed page that also echoes what someone typed (a search) can't
// leak it a byte at a time (BREACH). Templates print it with {{.CSRF}}.
type formToken struct{ secret []byte }

func (t formToken) String() string { return maskToken(t.secret) }

// formToken is what owner pages carry in their forms and data-csrf.
func (s *Server) formToken() formToken { return formToken{secret: s.csrf} }

func maskToken(secret []byte) string {
	out := make([]byte, 2*len(secret))
	if _, err := rand.Read(out[:len(secret)]); err != nil {
		panic("cairn: no randomness for the form token")
	}
	for i, b := range secret {
		out[len(secret)+i] = b ^ out[i]
	}
	return hex.EncodeToString(out)
}

// tokenMatches reports whether value is the owner form token: masked, as
// pages show it now, or plain, as forms opened before masking carry it.
func (s *Server) tokenMatches(value string) bool {
	switch len(value) {
	case 4 * len(s.csrf):
		raw, err := hex.DecodeString(value)
		if err != nil {
			return false
		}
		pad, masked := raw[:len(s.csrf)], raw[len(s.csrf):]
		for i := range masked {
			masked[i] ^= pad[i]
		}
		return subtle.ConstantTimeCompare(masked, s.csrf) == 1
	case 2 * len(s.csrf):
		return subtle.ConstantTimeCompare([]byte(value), []byte(hex.EncodeToString(s.csrf))) == 1
	}
	return false
}

// sameOrigin reports whether a form post came from one of Cairn's own pages
// on this listener. A post that fails it is refused outright.
func sameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https") {
		return false
	}
	// The listener's Host allow-list (hosts.go) applies to the origin too.
	if policy, ok := r.Context().Value(hostPolicyKey{}).(*hostPolicy); ok && !policy.allows(origin.Host) {
		return false
	}
	return true
}

// freshToken reports whether a parsed form carries the current token. A post
// from Cairn's own origin with an old token is stale, not hostile: its form
// shows again with the person's input and asks them to submit again.
func (s *Server) freshToken(r *http.Request) bool {
	return s.tokenMatches(r.FormValue("csrf"))
}

func (s *Server) ownerRequest(r *http.Request) bool {
	return sameOrigin(r) && s.freshToken(r)
}

// Shared form rules, worded for people rather than agents.

const (
	linkNameRule   = "Use letters, numbers, dashes or underscores, starting with a letter or number, up to 64 characters."
	linkNameTaken  = "Another page already uses this link. Choose a different one."
	titleMissing   = "Give the page a title."
	titleTooLong   = "Shorten the title to 300 characters or fewer."
	passwordShort  = "Use at least 8 characters."
	passwordLong   = "That password is too long. Use 72 characters or fewer."
	minPasswordLen = 8
)

// titleProblem checks a page title the way publication will.
func titleProblem(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return titleMissing
	}
	if len(title) > 300 || !utf8.ValidString(title) {
		return titleTooLong
	}
	return ""
}

// linkNameProblem checks a link name a person typed. Empty names are fine:
// Cairn chooses one. It returns the status and message for the field.
func (s *Server) linkNameProblem(name, exceptID string) (int, string) {
	if name == "" {
		return 0, ""
	}
	if slugPattern.MatchString(name) && !validSlug(name) {
		return http.StatusBadRequest, "Cairn uses “" + strings.ToLower(name) + "” itself. Choose another link name."
	}
	if !validSlug(name) {
		return http.StatusBadRequest, linkNameRule
	}
	var existing string
	// Names that differ only in letter case count as taken, as in the API.
	err := s.db.QueryRow(`SELECT id FROM pages WHERE (lower(public_slug)=lower(?1) OR lower(slug)=lower(?1)) AND id!=?2`, name, exceptID).Scan(&existing)
	if err == nil {
		return http.StatusConflict, linkNameTaken
	}
	return 0, ""
}

// passwordProblem checks a share password. bcrypt reads at most 72 bytes.
func passwordProblem(password string) string {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return passwordShort
	}
	if len(password) > 72 {
		return passwordLong
	}
	return ""
}

// publishProblem turns a failed savePublication response into the form
// field it concerns and a plain message. The API keeps its own wording.
func publishProblem(body []byte) (field, message string) {
	var e APIError
	_ = json.Unmarshal(body, &e)
	// API messages start with the field they concern, so a bare message
	// (the editor's preview sends only that) still names its field.
	field, reason := e.Field, ""
	if len(e.Errors) > 0 {
		field, reason = e.Errors[0].Field, e.Errors[0].Code
	}
	if field == "" {
		field, _, _ = strings.Cut(e.Message, " ")
	}
	switch e.Code {
	case ErrSlugConflict, ErrPublicSlugUnavailable:
		return "slug", linkNameTaken
	case ErrSharingUnavailable:
		return "access", "Public links aren’t set up on this server, so the page wasn’t saved. Keep it on your devices, or set up sharing first."
	case ErrStorageUnavailable, ErrCleanupPending, ErrLinkAllocationFailed:
		return "", "Cairn couldn’t save the page because its storage didn’t answer. Try again in a moment."
	case ErrValidationFailed:
		switch {
		case field == "slug" || field == "public_slug":
			return "slug", linkNameRule
		case strings.HasPrefix(field, "assets"):
			return "images", "Images must be PNG, JPEG, GIF or WebP files of 4 MB or less, and no more than 32 of them."
		case reason == FieldInvalidChart || strings.Contains(e.Message, "echarts block"):
			if _, detail, ok := strings.Cut(e.Message, "echarts block"); ok {
				return "", "A chart’s options aren’t valid JSON: echarts block" + strings.TrimSuffix(detail, ".") + "."
			}
		case field == "markdown" || field == "html" || field == "original_request" || reason == FieldTooLong:
			return "", "This page is too long to save. Pages can hold up to 2 MB of text."
		}
	}
	return "", "Cairn couldn’t save the page. Check it and try again."
}

// Guides that say how to fix what Cairn itself can't: the server's mail
// account lives in its environment, not in Settings.
const (
	kindleGuide         = "https://github.com/rowesk/cairn/blob/main/docs/kindle.md"
	mailTroubleshooting = "https://github.com/rowesk/cairn/blob/main/docs/troubleshooting.md#send-to-kindle"
)

// savedNotice confirms an edit when the reader opens after the editor saved
// it (/<slug>?saved=1). It is nil otherwise.
func savedNotice(r *http.Request) *notice {
	if r.URL.Query().Get("saved") == "1" {
		return succeeded("Saved.")
	}
	return nil
}

// kindleNotice turns a Send to Kindle result into its notice. Failures offer
// the next step: send again, or read how to fix the mail account. reader is true
// when the notice shows in the reader rather than the library.
func (s *Server) kindleNotice(result, slug string, reader bool) *notice {
	text, ok := kindleNotices[result]
	if !ok {
		return nil
	}
	var retry *noticeRetry
	if slug != "" && s.cfg.Mail.enabled() {
		retry = &noticeRetry{Action: "/manage/" + slug, Label: "Try again", Fields: map[string]string{"csrf": s.formToken().String(), "action": "kindle"}}
		if reader {
			retry.Fields["return"] = "reader"
		}
	}
	switch result {
	case "sent":
		return succeeded(text)
	case "unreachable", "signin", "refused", "busy", "failed":
		n := failed(text)
		// Try again only where sending again can work. The mail account is
		// the server's, not a setting in Cairn, so the guide says how to fix it.
		if kindleRetries[result] {
			n.Retry = retry
		}
		n.Link, n.LinkText = mailTroubleshooting, "Mail troubleshooting"
		if result == "refused" {
			n.Link, n.LinkText = "/settings#kindle", "Kindle address"
		}
		return n
	case "expired":
		n := informed(text)
		if retry != nil {
			retry.Label = "Send to Kindle"
		}
		n.Retry = retry
		return n
	}
	return failed(text)
}
