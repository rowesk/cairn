package cairn

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// The private publishing API under /api: create, replace, list, read and
// archive pages, the self-description at /api and /api/openapi.json, and the
// health check. Every response is JSON. Nothing here is on the public
// listener except /healthz, which sharing.go routes itself.

// Limits, in bytes unless named otherwise. The validator, the index, the
// OpenAPI document and the setup text all read these.
const (
	maxRequestBytes         = 16 << 20
	maxTitleBytes           = 300
	maxAgentBytes           = 100
	maxOriginalRequestBytes = 32 << 10
	maxSummaryBytes         = 1000
	maxSummaryWords         = 40
	maxSourceBytes          = 80
	maxContentBytes         = 2 << 20
	maxAssetBytes           = 4 << 20
	maxAssets               = 32
	maxSlugChars            = 64
	defaultListLimit        = 20
	maxListLimit            = 100
	idempotencyLifetime     = 24 * time.Hour
	maxIdempotencyKey       = 255
)

var (
	pageTemplates     = []string{"report", "comparison", "visual"}
	pageCapabilities  = []string{"mermaid", "echarts"}
	imageTypes        = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}
	reservedSlugs     = []string{"settings", "new", "manage", "import", "api", "healthz", "library", "assets", "admin", "login", "logout"}
	sharingFieldNames = []string{"share", "share_enabled", "shared", "public", "public_slug", "public_url", "password"}
	readOnlyFields    = []string{"id", "url", "private_url", "created_at", "updated_at", "format", "source", "editable", "warnings", "warning", "archived"}
)

// fieldSpec describes one field of a publication. The decoder, the OpenAPI
// document and the docs are built from this one table.
type fieldSpec struct {
	Name, Kind  string // Kind: string, strings or assets
	MaxBytes    int
	MaxWords    int
	MaxItems    int
	Allowed     []string
	Description string
}

var publicationFields = []fieldSpec{
	{Name: "title", Kind: "string", MaxBytes: maxTitleBytes, Description: "Required. A specific title that says what the page covers or concludes."},
	{Name: "agent", Kind: "string", MaxBytes: maxAgentBytes, Description: "The byline. Required with the installation token; an agent key sets its own byline and ignores this."},
	{Name: "original_request", Kind: "string", MaxBytes: maxOriginalRequestBytes, Description: "Required. The owner's request in their own words. Private: never shown on shared pages."},
	{Name: "request_summary", Kind: "string", MaxBytes: maxSummaryBytes, MaxWords: maxSummaryWords, Description: "The request from the owner's side in at most 40 words. Shown in the private library. On replacement, leaving it out keeps the saved one."},
	{Name: "request_source", Kind: "string", MaxBytes: maxSourceBytes, Description: "One line naming the app, such as Claude Code. Private. On replacement, leaving it out keeps the saved one."},
	{Name: "markdown", Kind: "string", MaxBytes: maxContentBytes, Description: "The page as Markdown. Send exactly one of markdown or html. Cairn keeps the Markdown so the page can be read back and edited."},
	{Name: "html", Kind: "string", MaxBytes: maxContentBytes, Description: "The page as a complete HTML document with its own layout. It runs in a sandbox with no network access."},
	{Name: "template", Kind: "string", Allowed: pageTemplates, Description: "Markdown layout. report (default) for reading, comparison for tables, visual for images, diagrams and charts. Ignored with html."},
	{Name: "slug", Kind: "string", MaxBytes: maxSlugChars, Description: "The page's address. Leave it out and Cairn chooses a short one. 1 to 64 letters, digits, hyphens or underscores, starting with a letter or digit."},
	{Name: "capabilities", Kind: "strings", Allowed: pageCapabilities, Description: "For html pages: bundled libraries to load before the page's scripts. Markdown loads them by itself."},
	{Name: "assets", Kind: "assets", MaxItems: maxAssets, MaxBytes: maxAssetBytes, Description: "Images by file name, each as base64. PNG, JPEG, GIF or WebP, 4 MiB each, at most 32. Refer to one as ?asset=name.png."},
}

var assetsAllowed = []string{"png", "jpeg", "gif", "webp"}

// apiWarning tells a publisher what Cairn changed or could not do while
// saving a page. The page is saved either way.
type apiWarning struct {
	Code       string   `json:"code" doc:"dropped_html, agent_ignored, template_ignored, private_url_unset or storage_cleanup_pending."`
	Message    string   `json:"message" doc:"What happened, in plain words."`
	Tags       []string `json:"tags,omitempty" doc:"For dropped_html: the HTML elements Cairn removed."`
	Attributes []string `json:"attributes,omitempty" doc:"For dropped_html: the attributes Cairn removed. href means an unsafe address was removed."`
}

var warningCodes = []string{"dropped_html", "agent_ignored", "template_ignored", "private_url_unset", "storage_cleanup_pending"}

// apiPage is a page as the API describes it.
type apiPage struct {
	ID              string   `json:"id" doc:"Cairn's internal identifier. Routes use slug."`
	Slug            string   `json:"slug" doc:"The page's address on this library."`
	URL             string   `json:"url" doc:"The page's path, such as /k3T."`
	PrivateURL      *string  `json:"private_url" doc:"The full private address, when the server sets a private base URL. Otherwise null: join url to the address you used."`
	PublicURL       *string  `json:"public_url" doc:"The public address while the owner shares the page, otherwise null."`
	Title           string   `json:"title"`
	Agent           string   `json:"agent" doc:"The byline."`
	Format          string   `json:"format" enum:"markdown,html,file" doc:"How the page was written."`
	Template        *string  `json:"template" enum:"report,comparison,visual" doc:"The Markdown template, or null for html and files."`
	OriginalRequest string   `json:"original_request"`
	RequestSummary  string   `json:"request_summary"`
	RequestSource   string   `json:"request_source"`
	Assets          []string `json:"assets" doc:"The page's image names. Each is served at /_assets/{slug}/{name}."`
	Shared          bool     `json:"shared" doc:"True while the owner shares the page by public link."`
	Archived        bool     `json:"archived"`
	Editable        bool     `json:"editable" doc:"True when Cairn kept Markdown it can open in its editor."`
	CreatedAt       string   `json:"created_at" doc:"RFC 3339, UTC."`
	UpdatedAt       string   `json:"updated_at" doc:"When the content, title or archive state last changed. RFC 3339, UTC."`
}

// apiSource is what Cairn kept of how a page was written.
type apiSource struct {
	Format      string  `json:"format" enum:"markdown,html,file"`
	Name        string  `json:"name,omitempty" doc:"The kept file's name."`
	Text        *string `json:"text" doc:"The Markdown or HTML, or null when Cairn did not keep it or it is not text."`
	DownloadURL string  `json:"download_url,omitempty" doc:"Where the original file downloads, for imported files."`
}

// apiPageRead is GET /api/pages/{slug}.
type apiPageRead struct {
	apiPage
	Source apiSource `json:"source"`
}

// apiPageWrite answers a create, replace or update.
type apiPageWrite struct {
	apiPage
	Warnings []apiWarning `json:"warnings" doc:"What Cairn changed or could not do. Empty when nothing."`
	// Warning is the single cleanup warning of API version 1.0.
	Warning string `json:"warning,omitempty" doc:"Deprecated: read warnings."`
}

// apiMatch says where a search matched a page.
type apiMatch struct {
	In         string   `json:"in" enum:"title,request,agent,page" doc:"Where the words were found."`
	Text       string   `json:"text" doc:"The matching words in context."`
	Highlights [][2]int `json:"highlights" doc:"Matched spans in text, as [start, end) character offsets."`
}

// apiListItem is one page in a list.
type apiListItem struct {
	Slug           string    `json:"slug"`
	URL            string    `json:"url"`
	PrivateURL     *string   `json:"private_url"`
	Title          string    `json:"title"`
	Agent          string    `json:"agent"`
	RequestSummary string    `json:"request_summary"`
	Excerpt        string    `json:"excerpt" doc:"The page's first real paragraph."`
	Shared         bool      `json:"shared"`
	Archived       bool      `json:"archived"`
	Editable       bool      `json:"editable"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
	Match          *apiMatch `json:"match,omitempty" doc:"With q: where the words matched."`
}

// apiList is GET /api/pages.
type apiList struct {
	Pages      []apiListItem `json:"pages"`
	Total      int           `json:"total" doc:"How many pages match in all."`
	HasMore    bool          `json:"has_more"`
	NextCursor *string       `json:"next_cursor" doc:"Pass as cursor for the next pages, or null at the end."`
}

// apiHealth is GET /healthz. The public listener answers with status only.
type apiHealth struct {
	Status   string `json:"status" enum:"ok,unavailable"`
	Version  string `json:"version,omitempty"`
	Database string `json:"database,omitempty" enum:"ok,unavailable"`
	Storage  string `json:"storage,omitempty" enum:"ok,unavailable"`
}

// publishContext carries what the API knows about a publication into
// savePublication, which the editor and import share.
type publishContext struct {
	cred     credential
	warnings []apiWarning
}

type publishContextKey struct{}

func publishContextOf(r *http.Request) *publishContext {
	c, _ := r.Context().Value(publishContextKey{}).(*publishContext)
	return c
}

// api routes everything under /api.
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	switch path := r.URL.Path; {
	case path == "/api":
		s.apiIndex(w, r)
	case path == "/api/openapi.json":
		s.apiOpenAPI(w, r)
	case path == "/api/pages" || strings.HasPrefix(path, "/api/pages/") && !strings.Contains(strings.TrimPrefix(path, "/api/pages/"), "/"):
		s.apiPages(w, r)
	default:
		writeAPIError(w, http.StatusNotFound, ErrNotFound, "No API route at "+path+". GET /api lists them.", false)
	}
}

// apiPages answers /api/pages and /api/pages/{slug}.
func (s *Server) apiPages(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "none") {
		writeAPIError(w, http.StatusForbidden, ErrBrowserOriginForbidden, "Browsers cannot use the API. Send the request from a command-line or server-side HTTP client.", false)
		return
	}
	cred, failure := s.authenticate(r)
	if failure != "" {
		noteRequest(r, "auth failure", slog.String("reason", failure))
		w.Header().Set("WWW-Authenticate", `Bearer realm="cairn"`)
		switch failure {
		case authMissing:
			writeAPIError(w, http.StatusUnauthorized, ErrAuthenticationRequired, "Send your agent key in an Authorization: Bearer header.", false)
		case authMalformed:
			writeAPIError(w, http.StatusUnauthorized, ErrAuthenticationRequired, "The Authorization header must be Bearer followed by your agent key.", false)
		default:
			writeAPIError(w, http.StatusUnauthorized, ErrInvalidCredential, "This key is not valid. It may have been rotated or revoked; ask the owner for a new one from Settings, Authors.", false)
		}
		return
	}
	slug, one := strings.CutPrefix(r.URL.Path, "/api/pages/")
	if !one {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.apiList(w, r, cred)
		case http.MethodPost:
			s.apiWrite(w, r, cred, "")
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, r.Method+" is not allowed on /api/pages. Use GET to list or POST to publish.", false)
		}
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.apiRead(w, r, cred, slug)
	case http.MethodPut, http.MethodPatch:
		s.apiWrite(w, r, cred, slug)
	case http.MethodDelete:
		w.Header().Set("Allow", "GET, HEAD, PUT, PATCH")
		writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, "Agents cannot delete pages. Archive one with PATCH and {\"archived\": true}; the owner can delete it from Manage page.", false)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT, PATCH")
		writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, r.Method+" is not allowed on a page. Use GET, PUT or PATCH.", false)
	}
}

// apiWrite reads a create, replace or update body and runs it once per
// Idempotency-Key.
func (s *Server) apiWrite(w http.ResponseWriter, r *http.Request, cred credential, slug string) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, ErrUnsupportedMediaType, "Send the body as JSON with Content-Type: application/json.", false)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		if isPayloadTooLarge(err) {
			writeAPIProblems(w, http.StatusRequestEntityTooLarge, ErrPayloadTooLarge, []FieldError{{Code: FieldTooLarge, Message: "The request is over the 16 MiB limit. Send smaller images or fewer of them.", Limit: maxRequestBytes, Unit: "bytes"}})
		} else {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidJSON, "Cairn could not read the request body. Send it again.", true)
		}
		return
	}
	run := func(w http.ResponseWriter) {
		switch {
		case r.Method == http.MethodPatch:
			s.apiUpdate(w, r, cred, slug, body)
		default:
			s.apiPublish(w, r, cred, slug, body)
		}
	}
	s.idempotent(w, r, cred, body, run)
}

// apiPublish creates (slug empty) or replaces a page.
func (s *Server) apiPublish(w http.ResponseWriter, r *http.Request, cred credential, slug string, body []byte) {
	raw, fail := readObject(body)
	if fail != nil {
		writeAPIErrorBody(w, http.StatusBadRequest, *fail)
		return
	}
	in, problems := decodePublication(raw)
	ctx := &publishContext{cred: cred}
	if !cred.installation() {
		if strings.TrimSpace(in.Agent) != "" && in.Agent != cred.Agent {
			ctx.warnings = append(ctx.warnings, apiWarning{Code: "agent_ignored", Message: "Your key signs pages as " + cred.Agent + ", so agent was ignored."})
		}
		in.Agent = cred.Agent
	}
	if strings.TrimSpace(in.HTML) != "" && in.Template != "" && slices.Contains(pageTemplates, in.Template) {
		ctx.warnings = append(ctx.warnings, apiWarning{Code: "template_ignored", Message: "template applies only to Markdown, so Cairn kept the HTML as sent."})
	}
	if in.Markdown != "" {
		// Kept so the page can be read back through the API and edited in Cairn.
		in.Source = &sourceFile{Name: "page.md", Data: []byte(in.Markdown)}
	}
	if len(problems) > 0 {
		// Report the field problems the decoder could not see too, in one reply.
		more, _ := preparePage(&in)
		writeAPIProblems(w, http.StatusBadRequest, ErrValidationFailed, mergeProblems(problems, more))
		return
	}
	s.savePublication(w, r.WithContext(context.WithValue(r.Context(), publishContextKey{}, ctx)), in)
}

// mergeProblems keeps one problem per field, in field order.
func mergeProblems(first, second []FieldError) []FieldError {
	seen := map[string]bool{}
	for _, p := range first {
		seen[p.Field] = true
	}
	for _, p := range second {
		if !seen[p.Field] {
			first = append(first, p)
		}
	}
	order := map[string]int{}
	for i, f := range publicationFields {
		order[f.Name] = i
	}
	// Fields Cairn does not take come first: a misspelt field often explains
	// why a required one is missing.
	rank := func(p FieldError) int {
		switch p.Code {
		case FieldUnknown, FieldReadOnly, FieldNotAllowed:
			return -1
		}
		name, _, _ := strings.Cut(strings.SplitN(p.Field, "[", 2)[0], ".")
		if i, ok := order[name]; ok {
			return i
		}
		return len(order)
	}
	sort.SliceStable(first, func(i, j int) bool { return rank(first[i]) < rank(first[j]) })
	return first
}

// readObject parses a request body that must be one JSON object.
func readObject(body []byte) (map[string]json.RawMessage, *APIError) {
	invalid := func(message string, line, column int) *APIError {
		return &APIError{Code: ErrInvalidJSON, Message: message, Errors: []FieldError{{Code: FieldSyntax, Message: message, Line: line, Column: column}}}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, invalid("The body is empty. Send one JSON object.", 0, 0)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		var syntax *json.SyntaxError
		var kind *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntax):
			// Offset counts the bytes read, including the one that failed.
			line, column := position(body, int(syntax.Offset)-1)
			return nil, invalid(fmt.Sprintf("The body is not valid JSON: %s at line %d, column %d.", syntax.Error(), line, column), line, column)
		case errors.As(err, &kind):
			return nil, invalid("The body must be one JSON object, not "+article(kind.Value)+".", 0, 0)
		case errors.Is(err, io.ErrUnexpectedEOF):
			line, column := position(body, len(body))
			return nil, invalid(fmt.Sprintf("The body is not valid JSON: it ends early, at line %d, column %d.", line, column), line, column)
		}
		return nil, invalid("The body is not valid JSON: "+err.Error()+".", 0, 0)
	}
	if raw == nil {
		return nil, invalid("The body must be one JSON object, not null.", 0, 0)
	}
	end := int(decoder.InputOffset())
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		rest := body[end:]
		end += len(rest) - len(bytes.TrimLeft(rest, " \t\r\n"))
		line, column := position(body, end)
		return nil, invalid(fmt.Sprintf("Send one JSON object. More follows it at line %d, column %d.", line, column), line, column)
	}
	return raw, nil
}

func position(body []byte, offset int) (line, column int) {
	offset = min(max(offset, 0), len(body))
	before := body[:offset]
	line = bytes.Count(before, []byte("\n")) + 1
	column = utf8.RuneCount(before[bytes.LastIndexByte(before, '\n')+1:]) + 1
	return line, column
}

func article(kind string) string {
	switch kind {
	case "array", "object":
		return "an " + kind
	}
	return "a " + kind
}

// jsonKind names the JSON type of a raw value for messages.
func jsonKind(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "nothing"
	}
	switch trimmed[0] {
	case '"':
		return "a string"
	case '{':
		return "an object"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	}
	return "a number"
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// takeString moves one string field out of raw. null counts as left out.
func takeString(raw map[string]json.RawMessage, name string, dst *string, problems *[]FieldError) {
	value, ok := raw[name]
	delete(raw, name)
	if !ok || isNull(value) {
		return
	}
	if err := json.Unmarshal(value, dst); err != nil {
		*problems = append(*problems, FieldError{Code: FieldWrongType, Field: name, Message: name + " must be a string, not " + jsonKind(value) + "."})
	}
}

func takeBool(raw map[string]json.RawMessage, name string, dst **bool, problems *[]FieldError) {
	value, ok := raw[name]
	delete(raw, name)
	if !ok || isNull(value) {
		return
	}
	var b bool
	if err := json.Unmarshal(value, &b); err != nil {
		*problems = append(*problems, FieldError{Code: FieldWrongType, Field: name, Message: name + " must be true or false, not " + jsonKind(value) + "."})
		return
	}
	*dst = &b
}

// leftovers reports every field the request had that the route does not
// accept, with a likely meaning when there is one.
func leftovers(raw map[string]json.RawMessage, accepted []string, problems *[]FieldError) {
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		shown := clip(name, 40)
		switch {
		case slices.Contains(sharingFieldNames, name):
			*problems = append(*problems, FieldError{Code: FieldNotAllowed, Field: shown, Message: shown + " is not available to agents. Only the owner shares pages, from Cairn."})
		case name == "archived" && slices.Contains(accepted, "title") && !slices.Contains(accepted, "archived"):
			*problems = append(*problems, FieldError{Code: FieldReadOnly, Field: shown, Message: "archived changes with PATCH /api/pages/{slug}. Leave it out here."})
		case slices.Contains(readOnlyFields, name):
			*problems = append(*problems, FieldError{Code: FieldReadOnly, Field: shown, Message: shown + " is read-only. Leave it out."})
		default:
			message := shown + " is not a field Cairn accepts."
			if guess := closest(name, accepted); guess != "" {
				message += " Did you mean " + guess + "?"
			}
			*problems = append(*problems, FieldError{Code: FieldUnknown, Field: shown, Message: message, Allowed: accepted})
		}
	}
}

// synonyms are names other APIs use for Cairn's fields and parameters.
var synonyms = map[string]string{
	"body": "markdown", "content": "markdown", "text": "markdown", "md": "markdown",
	"request": "original_request", "prompt": "original_request", "question": "original_request",
	"summary": "request_summary", "source": "request_source", "app": "request_source",
	"author": "agent", "byline": "agent", "name": "title", "heading": "title",
	"images": "assets", "attachments": "assets", "files": "assets", "layout": "template",
	"path": "slug", "id": "slug", "search": "q", "query": "q", "term": "q",
	"page": "cursor", "offset": "cursor", "after": "cursor", "starting_after": "cursor",
	"per_page": "limit", "count": "limit", "size": "limit", "page_size": "limit",
}

// closest returns the accepted name nearest to name, if it is a likely typo.
func closest(name string, accepted []string) string {
	best, bestDistance := "", 3
	lower := strings.ToLower(name)
	if guess, ok := synonyms[lower]; ok && slices.Contains(accepted, guess) {
		return guess
	}
	for _, candidate := range accepted {
		if strings.Contains(candidate, lower) && len(lower) >= 4 || strings.Contains(lower, candidate) && len(candidate) >= 4 {
			return candidate
		}
		if d := editDistance(lower, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

// decodePublication turns a parsed body into a publication, collecting a
// problem for every field of the wrong type and every field it does not know.
func decodePublication(raw map[string]json.RawMessage) (publication, []FieldError) {
	var in publication
	var problems []FieldError
	accepted := make([]string, 0, len(publicationFields))
	for _, f := range publicationFields {
		accepted = append(accepted, f.Name)
	}
	takeString(raw, "title", &in.Title, &problems)
	takeString(raw, "agent", &in.Agent, &problems)
	takeString(raw, "original_request", &in.OriginalRequest, &problems)
	takeString(raw, "request_summary", &in.RequestSummary, &problems)
	takeString(raw, "request_source", &in.RequestSource, &problems)
	takeString(raw, "markdown", &in.Markdown, &problems)
	takeString(raw, "html", &in.HTML, &problems)
	takeString(raw, "template", &in.Template, &problems)
	takeString(raw, "slug", &in.Slug, &problems)
	if value, ok := raw["capabilities"]; ok && !isNull(value) {
		var items []json.RawMessage
		if json.Unmarshal(value, &items) != nil {
			problems = append(problems, FieldError{Code: FieldWrongType, Field: "capabilities", Message: "capabilities must be an array of strings, not " + jsonKind(value) + ".", Allowed: pageCapabilities})
		}
		for i, item := range items {
			var name string
			if json.Unmarshal(item, &name) != nil {
				field := "capabilities[" + strconv.Itoa(i) + "]"
				problems = append(problems, FieldError{Code: FieldWrongType, Field: field, Message: field + " must be a string, not " + jsonKind(item) + ".", Allowed: pageCapabilities})
				continue
			}
			in.Capabilities = append(in.Capabilities, name)
		}
	}
	delete(raw, "capabilities")
	if value, ok := raw["assets"]; ok && !isNull(value) {
		var items map[string]json.RawMessage
		if json.Unmarshal(value, &items) != nil {
			problems = append(problems, FieldError{Code: FieldWrongType, Field: "assets", Message: "assets must be an object mapping file names to base64 strings, not " + jsonKind(value) + "."})
		}
		names := make([]string, 0, len(items))
		for name := range items {
			names = append(names, name)
		}
		sort.Strings(names)
		in.Assets = map[string][]byte{}
		for _, name := range names {
			field := "assets." + clip(name, 100)
			var encoded string
			if json.Unmarshal(items[name], &encoded) != nil {
				problems = append(problems, FieldError{Code: FieldWrongType, Field: field, Message: field + " must be a base64 string, not " + jsonKind(items[name]) + "."})
				continue
			}
			data, err := decodeImage(encoded)
			if err != nil {
				problems = append(problems, FieldError{Code: FieldInvalidBase64, Field: field, Message: field + " is not valid base64: " + strings.TrimPrefix(err.Error(), "illegal base64 data ") + "."})
				continue
			}
			in.Assets[name] = data
		}
	}
	delete(raw, "assets")
	leftovers(raw, accepted, &problems)
	return in, problems
}

// decodeImage reads base64 with or without padding, in either alphabet,
// with line breaks, or as a data: URL.
func decodeImage(s string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(s, "data:"); ok {
		if _, data, found := strings.Cut(rest, ";base64,"); found {
			s = data
		}
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(s); err == nil {
			return data, nil
		}
	}
	_, err := base64.StdEncoding.DecodeString(s)
	return nil, err
}

// checkFields finds every problem in a publication's fields, in field order.
// It does not render Markdown; preparePage does.
func checkFields(in publication) []FieldError {
	var problems []FieldError
	add := func(p FieldError) { problems = append(problems, p) }
	tooLong := func(field string, size, limit int) {
		add(FieldError{Code: FieldTooLong, Field: field, Message: fmt.Sprintf("%s is %d bytes; the limit is %s.", field, size, bytesLimit(limit)), Limit: limit, Actual: size, Unit: "bytes"})
	}
	switch {
	case strings.TrimSpace(in.Title) == "":
		add(FieldError{Code: FieldRequired, Field: "title", Message: "title is required. Give the page a short, specific title."})
	case len(in.Title) > maxTitleBytes:
		tooLong("title", len(in.Title), maxTitleBytes)
	}
	switch {
	case strings.TrimSpace(in.Agent) == "":
		add(FieldError{Code: FieldRequired, Field: "agent", Message: "agent is required with the installation token. Send the byline to show, or publish with an agent key."})
	case len(in.Agent) > maxAgentBytes:
		tooLong("agent", len(in.Agent), maxAgentBytes)
	}
	switch {
	case strings.TrimSpace(in.OriginalRequest) == "" && !in.OwnerWritten:
		add(FieldError{Code: FieldRequired, Field: "original_request", Message: "original_request is required. Send the owner's request in their own words."})
	case len(in.OriginalRequest) > maxOriginalRequestBytes:
		tooLong("original_request", len(in.OriginalRequest), maxOriginalRequestBytes)
	}
	if words := len(strings.Fields(in.RequestSummary)); words > maxSummaryWords {
		add(FieldError{Code: FieldTooManyWords, Field: "request_summary", Message: fmt.Sprintf("request_summary is %d words; the limit is %d words.", words, maxSummaryWords), Limit: maxSummaryWords, Actual: words, Unit: "words"})
	} else if len(in.RequestSummary) > maxSummaryBytes {
		tooLong("request_summary", len(in.RequestSummary), maxSummaryBytes)
	}
	if len(in.RequestSource) > maxSourceBytes {
		tooLong("request_source", len(in.RequestSource), maxSourceBytes)
	} else if strings.ContainsAny(in.RequestSource, "\r\n") {
		add(FieldError{Code: FieldMultipleLines, Field: "request_source", Message: "request_source must be one line, such as Claude Code."})
	}
	hasMarkdown, hasHTML := strings.TrimSpace(in.Markdown) != "", strings.TrimSpace(in.HTML) != ""
	switch {
	case !hasMarkdown && !hasHTML:
		add(FieldError{Code: FieldRequired, Field: "markdown", Message: "markdown or html is required. Send the page in one of them."})
	case hasMarkdown && hasHTML:
		add(FieldError{Code: FieldConflict, Field: "html", Message: "html and markdown were both sent. Send one of them."})
	case len(in.Markdown) > maxContentBytes:
		tooLong("markdown", len(in.Markdown), maxContentBytes)
	case len(in.HTML) > maxContentBytes:
		tooLong("html", len(in.HTML), maxContentBytes)
	}
	if in.Template != "" && !slices.Contains(pageTemplates, in.Template) {
		add(FieldError{Code: FieldInvalidValue, Field: "template", Message: "template " + strconv.Quote(clip(in.Template, 40)) + " is not one of report, comparison or visual.", Actual: clip(in.Template, 40), Allowed: pageTemplates})
	}
	if in.Slug != "" {
		if p := slugProblem(in.Slug); p != nil {
			add(*p)
		}
	}
	for i, capability := range in.Capabilities {
		if !slices.Contains(pageCapabilities, capability) {
			field := "capabilities[" + strconv.Itoa(i) + "]"
			add(FieldError{Code: FieldInvalidValue, Field: field, Message: field + " " + strconv.Quote(clip(capability, 40)) + " is not one of mermaid or echarts.", Actual: clip(capability, 40), Allowed: pageCapabilities})
		}
	}
	if len(in.Assets) > maxAssets {
		add(FieldError{Code: FieldTooManyItems, Field: "assets", Message: fmt.Sprintf("assets has %d images; the limit is %d.", len(in.Assets), maxAssets), Limit: maxAssets, Actual: len(in.Assets), Unit: "items"})
	}
	names := make([]string, 0, len(in.Assets))
	for name := range in.Assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := in.Assets[name]
		field := "assets." + clip(name, 100)
		switch {
		case !assetName.MatchString(name):
			add(FieldError{Code: FieldInvalidAssetName, Field: field, Message: field + ": name images with letters, digits, hyphens or underscores, up to 96 characters, then .png, .jpg, .jpeg, .gif or .webp.", Allowed: []string{"png", "jpg", "jpeg", "gif", "webp"}})
		case len(data) == 0:
			add(FieldError{Code: FieldEmptyAsset, Field: field, Message: field + " is empty. Send the image's bytes as base64."})
		case len(data) > maxAssetBytes:
			add(FieldError{Code: FieldTooLarge, Field: field, Message: fmt.Sprintf("%s is %d bytes; the limit is %s per image.", field, len(data), bytesLimit(maxAssetBytes)), Limit: maxAssetBytes, Actual: len(data), Unit: "bytes"})
		default:
			if media := http.DetectContentType(data); !slices.Contains(imageTypes, media) {
				kind, _, _ := strings.Cut(media, ";")
				add(FieldError{Code: FieldUnsupportedImage, Field: field, Message: field + " holds " + kind + ", not a PNG, JPEG, GIF or WebP image. Convert it first.", Actual: kind, Allowed: assetsAllowed})
			}
		}
	}
	return problems
}

// slugProblem checks a slug's form, not whether another page has it.
func slugProblem(slug string) *FieldError {
	shown := clip(slug, 70)
	if slugPattern.MatchString(slug) && slices.Contains(reservedSlugs, strings.ToLower(slug)) {
		return &FieldError{Code: FieldReservedSlug, Field: "slug", Message: "slug " + strconv.Quote(shown) + " is a name Cairn uses itself. Choose another, or leave slug out.", Actual: shown}
	}
	if !slugPattern.MatchString(slug) {
		return &FieldError{Code: FieldInvalidSlug, Field: "slug", Message: "slug " + strconv.Quote(shown) + " must be 1 to 64 letters, digits, hyphens or underscores, starting with a letter or digit.", Actual: shown, Limit: maxSlugChars, Unit: "bytes"}
	}
	return nil
}

func bytesLimit(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB (%d bytes)", n>>20, n)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB (%d bytes)", n>>10, n)
	}
	return fmt.Sprintf("%d bytes", n)
}

// preparePage checks a publication and, when it is Markdown, renders it in
// place. Chart problems are found even when other fields have problems, so
// one reply can name them all.
func preparePage(in *publication) ([]FieldError, renderResult) {
	problems := checkFields(*in)
	var result renderResult
	hasMarkdown, hasHTML := strings.TrimSpace(in.Markdown) != "", strings.TrimSpace(in.HTML) != ""
	if hasMarkdown && !hasHTML && len(in.Markdown) <= maxContentBytes && (in.Template == "" || slices.Contains(pageTemplates, in.Template)) {
		size := len(in.Markdown)
		rendered, err := renderPublication(in)
		var chart *chartError
		switch {
		case errors.As(err, &chart):
			problems = append(problems, FieldError{Code: FieldInvalidChart, Field: "markdown", Message: "markdown: " + chart.Error() + ".", Line: chart.Line, Block: chart.Block})
		case err != nil:
			problems = append(problems, FieldError{Code: FieldInvalidValue, Field: "markdown", Message: "markdown could not be rendered: " + err.Error() + "."})
		case len(in.HTML) > maxContentBytes:
			problems = append(problems, FieldError{Code: FieldTooLong, Field: "markdown", Message: fmt.Sprintf("markdown (%d bytes) renders to %d bytes of HTML; the limit is %s.", size, len(in.HTML), bytesLimit(maxContentBytes)), Limit: maxContentBytes, Actual: len(in.HTML), Unit: "bytes"})
		}
		result = rendered
	}
	if len(problems) == 0 {
		if err := addRuntime(in); err != nil {
			problems = append(problems, FieldError{Code: FieldInvalidValue, Field: "html", Message: "html could not be read: " + err.Error() + "."})
		}
	}
	return mergeProblems(nil, problems), result
}

// renderWarnings turns what the Markdown renderer dropped into a warning.
func renderWarnings(result renderResult) []apiWarning {
	if len(result.DroppedTags) == 0 && len(result.DroppedAttributes) == 0 {
		return nil
	}
	var parts []string
	if len(result.DroppedTags) > 0 {
		parts = append(parts, "elements "+strings.Join(result.DroppedTags, ", "))
	}
	if len(result.DroppedAttributes) > 0 {
		parts = append(parts, "attributes "+strings.Join(result.DroppedAttributes, ", "))
	}
	return []apiWarning{{Code: "dropped_html", Message: "Cairn removed raw HTML it does not allow from the Markdown: " + strings.Join(parts, "; ") + ". The rest of the page was saved.", Tags: result.DroppedTags, Attributes: result.DroppedAttributes}}
}

// privateURL is the full private address of a path, or nil when the server
// has no private base URL. It never comes from the request's Host.
func (s *Server) privateURL(path string) *string {
	if s.cfg.PrivateBaseURL == "" {
		return nil
	}
	full := strings.TrimRight(s.cfg.PrivateBaseURL, "/") + path
	return &full
}

// pageResource reads a page as the API describes it. Callers hold s.mu.
func (s *Server) pageResource(slug string) (apiPage, savedPage, error) {
	p, err := s.getPage(slug)
	if err != nil {
		return apiPage{}, p, err
	}
	var shared, archived bool
	var publicSlug sql.NullString
	var updated string
	if err := s.db.QueryRow(`SELECT share_enabled,archived,public_slug,updated_at FROM pages WHERE id=?`, p.ID).Scan(&shared, &archived, &publicSlug, &updated); err != nil {
		return apiPage{}, p, err
	}
	if updated == "" {
		updated = p.CreatedAt
	}
	out := apiPage{ID: p.ID, Slug: p.Slug, URL: "/" + p.Slug, PrivateURL: s.privateURL("/" + p.Slug), Title: p.Title, Agent: p.Agent, OriginalRequest: p.OriginalRequest, RequestSummary: p.RequestSummary, RequestSource: p.RequestSource, Assets: []string{}, Shared: shared, Archived: archived, Editable: editableSource(p.FileName), CreatedAt: p.CreatedAt, UpdatedAt: updated}
	if shared && s.cfg.PublicBaseURL != "" {
		route := publicSlug.String
		if route == "" {
			route = p.Slug
		}
		public := strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/" + route
		out.PublicURL = &public
	}
	dir := filepath.Join(s.cfg.DataDir, "objects", p.Object)
	entries, _ := os.ReadDir(filepath.Join(dir, "assets"))
	for _, entry := range entries {
		if assetName.MatchString(entry.Name()) {
			out.Assets = append(out.Assets, entry.Name())
		}
	}
	out.Format = "html"
	page, _ := os.ReadFile(filepath.Join(dir, "page.html"))
	if m := templatePattern.FindSubmatch(page); m != nil {
		layout := string(m[1])
		out.Template = &layout
	}
	switch {
	case editableSource(p.FileName):
		out.Format = "markdown"
	case p.FileName != "" && !strings.EqualFold(filepath.Ext(p.FileName), ".html") && !strings.EqualFold(filepath.Ext(p.FileName), ".htm"):
		out.Format = "file"
		out.Template = nil
	case out.Template != nil && p.FileName == "":
		// Markdown sent before Cairn kept a copy.
		out.Format = "markdown"
	}
	return out, p, nil
}

// runtimeTag matches the script tags addRuntime adds, so the HTML an agent
// reads back can be sent again without doubling them.
var runtimeTag = regexp.MustCompile(`<script src="/_runtime/(?:mermaid|echarts|boot)\.js"></script>`)

// pageSource reads what Cairn kept of how a page was written.
func (s *Server) pageSource(page apiPage, p savedPage) apiSource {
	dir := filepath.Join(s.cfg.DataDir, "objects", p.Object)
	source := apiSource{Format: page.Format, Name: p.FileName}
	text := func(data []byte) *string {
		if !utf8.Valid(data) || len(data) > maxContentBytes {
			return nil
		}
		t := string(data)
		return &t
	}
	switch {
	case p.FileName != "" && page.Format != "file":
		if data, err := os.ReadFile(filepath.Join(dir, "source.bin")); err == nil {
			source.Text = text(data)
		}
	case page.Format == "file":
		source.DownloadURL = "/" + p.Slug + "?download=1"
	case page.Format == "html":
		if data, err := os.ReadFile(filepath.Join(dir, "page.html")); err == nil {
			source.Text = text(runtimeTag.ReplaceAll(data, nil))
		}
	}
	return source
}

// noPage answers a slug the credential cannot reach. A page with another
// byline gets exactly the answer a missing page gets, so an agent key learns
// nothing about other authors' pages. replace adds how to publish instead.
func noPage(w http.ResponseWriter, slug string, replace bool) {
	message := "No page has the slug " + strconv.Quote(clip(slug, 70)) + "."
	if replace {
		message += " To publish a new page, POST to /api/pages."
	}
	writeAPIErrorField(w, http.StatusNotFound, ErrNotFound, "slug", message, false)
}

// apiRead answers GET and HEAD /api/pages/{slug}.
func (s *Server) apiRead(w http.ResponseWriter, r *http.Request, cred credential, slug string) {
	if !validSlug(slug) {
		noPage(w, slug, false)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	page, p, err := s.pageResource(slug)
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && !cred.reaches(page.Agent):
		noPage(w, slug, false)
		return
	case err != nil:
		s.storageFailure(w, r, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, apiPageRead{apiPage: page, Source: s.pageSource(page, p)})
}

// apiList answers GET /api/pages with the library's own search.
func (s *Server) apiList(w http.ResponseWriter, r *http.Request, cred credential) {
	values := r.URL.Query()
	var problems []FieldError
	q := libraryQuery{Text: strings.TrimSpace(values.Get("q")), Agent: values.Get("agent"), Limit: defaultListLimit}
	if len(q.Text) > 200 {
		problems = append(problems, FieldError{Code: FieldTooLong, Field: "q", Message: fmt.Sprintf("q is %d bytes; the limit is 200 bytes.", len(q.Text)), Limit: 200, Actual: len(q.Text), Unit: "bytes"})
	}
	if v := values.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxListLimit {
			problems = append(problems, FieldError{Code: FieldInvalidValue, Field: "limit", Message: fmt.Sprintf("limit must be a whole number from 1 to %d.", maxListLimit), Limit: maxListLimit, Actual: clip(v, 20)})
		} else {
			q.Limit = n
		}
	}
	switch v := values.Get("archived"); v {
	case "", "false", "0":
	case "true", "1":
		q.Archived = true
	default:
		problems = append(problems, FieldError{Code: FieldInvalidValue, Field: "archived", Message: "archived must be true or false.", Actual: clip(v, 20), Allowed: []string{"true", "false"}})
	}
	if v := values.Get("cursor"); v != "" {
		offset, ok := readCursor(v)
		if !ok {
			problems = append(problems, FieldError{Code: FieldInvalidValue, Field: "cursor", Message: "cursor is not one Cairn gave out. Pass next_cursor from the previous page of results."})
		}
		q.Offset = offset
	}
	accepted := []string{"q", "agent", "limit", "cursor", "archived"}
	var unknown []string
	for name := range values {
		if !slices.Contains(accepted, name) {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		shown := clip(name, 40)
		message := shown + " is not a parameter Cairn accepts."
		if guess := closest(name, accepted); guess != "" {
			message += " Did you mean " + guess + "?"
		}
		problems = append(problems, FieldError{Code: FieldUnknown, Field: shown, Message: message, Allowed: accepted})
	}
	if len(problems) > 0 {
		writeAPIProblems(w, http.StatusBadRequest, ErrValidationFailed, problems)
		return
	}
	if !cred.installation() {
		if q.Agent != "" && q.Agent != cred.Agent {
			writeAPIErrorField(w, http.StatusForbidden, ErrForbidden, "agent", "Your key lists only pages by "+cred.Agent+". Leave agent out.", false)
			return
		}
		q.Agent = cred.Agent
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items, total, err := s.listPages(q)
	if err != nil {
		s.storageFailure(w, r, err)
		return
	}
	extra, err := s.listExtras(items)
	if err != nil {
		s.storageFailure(w, r, err)
		return
	}
	out := apiList{Pages: []apiListItem{}, Total: total}
	for _, item := range items {
		e := extra[item.Slug]
		entry := apiListItem{Slug: item.Slug, URL: "/" + item.Slug, PrivateURL: s.privateURL("/" + item.Slug), Title: item.Title, Agent: item.Agent, RequestSummary: e.summary, Excerpt: item.Preview, Shared: item.Shared, Archived: item.Archived, Editable: item.Editable, CreatedAt: item.CreatedAt, UpdatedAt: e.updated}
		if q.Text != "" {
			entry.Match = matchOf(item)
		}
		out.Pages = append(out.Pages, entry)
	}
	if next := q.Offset + len(items); next < total && len(items) > 0 {
		out.HasMore = true
		cursor := base64.RawURLEncoding.EncodeToString([]byte("o" + strconv.Itoa(next)))
		out.NextCursor = &cursor
	}
	writeAPIJSON(w, http.StatusOK, out)
}

func readCursor(v string) (int, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(raw) < 2 || raw[0] != 'o' {
		return 0, false
	}
	n, err := strconv.Atoi(string(raw[1:]))
	return n, err == nil && n >= 0
}

type listExtra struct{ updated, summary string }

// listExtras reads the fields the library rows leave out.
func (s *Server) listExtras(items []libraryItem) (map[string]listExtra, error) {
	out := map[string]listExtra{}
	if len(items) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(items))
	for _, item := range items {
		args = append(args, item.Slug)
	}
	rows, err := s.db.Query(`SELECT slug,updated_at,created_at,request_summary FROM pages WHERE slug IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var slug, updated, created, summary string
		if err := rows.Scan(&slug, &updated, &created, &summary); err != nil {
			return nil, err
		}
		if updated == "" {
			updated = created
		}
		out[slug] = listExtra{updated: updated, summary: summary}
	}
	return out, rows.Err()
}

// matchOf turns the library's highlighted row into plain text and spans.
func matchOf(item libraryItem) *apiMatch {
	var in string
	var marked template.HTML
	switch {
	case item.Where == "In your request":
		in, marked = "request", item.Snippet
	case item.Where == "In the page":
		in, marked = "page", item.Snippet
	case item.TitleHTML != "":
		in, marked = "title", item.TitleHTML
	case item.AgentHTML != "":
		in, marked = "agent", item.AgentHTML
	default:
		return nil
	}
	text, spans := unmark(string(marked))
	return &apiMatch{In: in, Text: text, Highlights: spans}
}

// unmark reads text escaped and marked by marked() back into plain text and
// the character spans of each <mark>.
func unmark(s string) (string, [][2]int) {
	var out strings.Builder
	spans := [][2]int{}
	position, start := 0, 0
	for s != "" {
		switch {
		case strings.HasPrefix(s, "<mark>"):
			start, s = position, s[len("<mark>"):]
		case strings.HasPrefix(s, "</mark>"):
			spans = append(spans, [2]int{start, position})
			s = s[len("</mark>"):]
		default:
			end := strings.IndexByte(s[1:], '<') + 1
			if end == 0 {
				end = len(s)
			}
			chunk := html.UnescapeString(s[:end])
			out.WriteString(chunk)
			position += utf8.RuneCountInString(chunk)
			s = s[end:]
		}
	}
	return out.String(), spans
}

// apiUpdate answers PATCH /api/pages/{slug}: archive, restore or retitle.
func (s *Server) apiUpdate(w http.ResponseWriter, r *http.Request, cred credential, slug string, body []byte) {
	raw, fail := readObject(body)
	if fail != nil {
		writeAPIErrorBody(w, http.StatusBadRequest, *fail)
		return
	}
	var problems []FieldError
	var archived *bool
	var title *string
	takeBool(raw, "archived", &archived, &problems)
	if value, ok := raw["title"]; ok && !isNull(value) {
		var t string
		takeString(raw, "title", &t, &problems)
		title = &t
	}
	delete(raw, "title")
	leftovers(raw, []string{"archived", "title"}, &problems)
	if title != nil {
		switch {
		case strings.TrimSpace(*title) == "":
			problems = append(problems, FieldError{Code: FieldRequired, Field: "title", Message: "title cannot be empty. Leave it out to keep the current title."})
		case len(*title) > maxTitleBytes:
			problems = append(problems, FieldError{Code: FieldTooLong, Field: "title", Message: fmt.Sprintf("title is %d bytes; the limit is %s.", len(*title), bytesLimit(maxTitleBytes)), Limit: maxTitleBytes, Actual: len(*title), Unit: "bytes"})
		}
	}
	if len(problems) == 0 && archived == nil && title == nil {
		problems = append(problems, FieldError{Code: FieldRequired, Field: "archived", Message: "Send archived, title or both. To replace the page itself, use PUT."})
	}
	if len(problems) > 0 {
		writeAPIProblems(w, http.StatusBadRequest, ErrValidationFailed, problems)
		return
	}
	if !validSlug(slug) {
		noPage(w, slug, false)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.getPage(slug)
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && !cred.reaches(p.Agent):
		noPage(w, slug, false)
		return
	case err != nil:
		s.storageFailure(w, r, err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err == nil {
		if archived != nil {
			_, err = tx.Exec(`UPDATE pages SET archived=?,updated_at=? WHERE id=?`, *archived, now, p.ID)
		}
		if err == nil && title != nil {
			clean := strings.Join(strings.Fields(*title), " ")
			if _, err = tx.Exec(`UPDATE pages SET title=?,index_source=?,updated_at=? WHERE id=?`, clean, indexStamp(p.Object, clean, p.RequestSummary, p.OriginalRequest, p.Agent), now, p.ID); err == nil {
				_, err = tx.Exec(`UPDATE pages_fts SET title=? WHERE page_id=?`, indexable(clean), p.ID)
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	if err != nil {
		s.storageFailure(w, r, err)
		return
	}
	page, _, err := s.pageResource(slug)
	if err != nil {
		s.storageFailure(w, r, err)
		return
	}
	attrs := []slog.Attr{slog.String("slug", slug), slog.String("credential", cred.kind())}
	if archived != nil {
		attrs = append(attrs, slog.Bool("archived", *archived))
	}
	noteRequest(r, "update", attrs...)
	writeAPIJSON(w, http.StatusOK, apiPageWrite{apiPage: page, Warnings: s.writeWarnings(nil)})
}

// writeWarnings adds what every write says about the server itself.
func (s *Server) writeWarnings(warnings []apiWarning) []apiWarning {
	if warnings == nil {
		warnings = []apiWarning{}
	}
	if s.cfg.PrivateBaseURL == "" {
		warnings = append(warnings, apiWarning{Code: "private_url_unset", Message: "This server has no private base URL set, so private_url is null. Join url to the address you sent this request to."})
	}
	return warnings
}

func writeAPIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = apiEncoder(w).Encode(v)
}

// apiEncoder writes JSON for agents to read: "Q&A", not
// "Q\u0026A". Every response is application/json, never HTML.
func apiEncoder(w io.Writer) *json.Encoder {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder
}

// storageFailure answers a database or disk failure, and logs its cause.
func (s *Server) storageFailure(w http.ResponseWriter, r *http.Request, err error) {
	failRequest(r, err)
	writeAPIError(w, http.StatusInternalServerError, ErrStorageUnavailable, "Cairn's storage did not answer. Try again in a moment.", true)
}

// Idempotency: a write sent again with the same Idempotency-Key, from the
// same credential, within a day, returns the first result instead of
// running again. Only successful results are kept, so a corrected request
// can reuse its key.

func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, cred credential, body []byte, run func(http.ResponseWriter)) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		run(w)
		return
	}
	if len(key) > maxIdempotencyKey || strings.IndexFunc(key, func(c rune) bool { return c < 0x21 || c > 0x7e }) >= 0 {
		writeAPIProblems(w, http.StatusBadRequest, ErrValidationFailed, []FieldError{{Code: FieldInvalidValue, Field: "Idempotency-Key", Message: fmt.Sprintf("Idempotency-Key must be 1 to %d visible ASCII characters, such as a UUID.", maxIdempotencyKey), Limit: maxIdempotencyKey, Actual: len(key), Unit: "bytes"}})
		return
	}
	compact := new(bytes.Buffer)
	if json.Compact(compact, body) != nil {
		compact = bytes.NewBuffer(body)
	}
	sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), compact.Bytes()...))
	fingerprint := hex.EncodeToString(sum[:])
	held := cred.Scope + "\x00" + key
	s.idemMu.Lock()
	if s.idemHeld[held] {
		s.idemMu.Unlock()
		writeAPIError(w, http.StatusConflict, ErrIdempotencyInProgress, "A request with this Idempotency-Key is still running. Wait for it, then send again with the same key.", true)
		return
	}
	s.idemHeld[held] = true
	s.idemMu.Unlock()
	defer func() {
		s.idemMu.Lock()
		delete(s.idemHeld, held)
		s.idemMu.Unlock()
	}()
	cutoff := time.Now().Add(-idempotencyLifetime).UTC().Format(time.RFC3339)
	var stored struct {
		fingerprint, pageID string
		status              int
	}
	err := s.db.QueryRow(`SELECT fingerprint,status,page_id FROM idempotency_keys WHERE scope=? AND key=? AND created_at>=?`, cred.Scope, key, cutoff).Scan(&stored.fingerprint, &stored.status, &stored.pageID)
	switch {
	case err == nil && stored.fingerprint != fingerprint:
		writeAPIError(w, http.StatusUnprocessableEntity, ErrIdempotencyKeyReused, "This Idempotency-Key was already used for a different request. Use a new key for a new request.", false)
		return
	case err == nil:
		// The reply is rebuilt from the page as it is now: Cairn keeps only
		// which page the first request wrote, never its content.
		if s.replay(w, r, cred, stored.status, stored.pageID) {
			return
		}
		// The page has gone since; the request runs again.
		_, _ = s.db.Exec(`DELETE FROM idempotency_keys WHERE scope=? AND key=?`, cred.Scope, key)
	case !errors.Is(err, sql.ErrNoRows):
		s.storageFailure(w, r, err)
		return
	}
	recorder := &capture{header: http.Header{}, status: http.StatusOK}
	run(recorder)
	for name, values := range recorder.header {
		w.Header()[name] = values
	}
	w.WriteHeader(recorder.status)
	_, _ = w.Write(recorder.body.Bytes())
	if recorder.status >= 200 && recorder.status < 300 {
		if id, slim := slimReply(recorder.body.Bytes()); id != "" {
			now := time.Now().UTC().Format(time.RFC3339)
			_, _ = s.db.Exec(`DELETE FROM idempotency_keys WHERE created_at<?`, cutoff)
			_, _ = s.db.Exec(`INSERT OR REPLACE INTO idempotency_keys(scope,key,fingerprint,status,response,created_at,page_id) VALUES(?,?,?,?,?,?,?)`, cred.Scope, key, fingerprint, recorder.status, slim, now, id)
		}
	}
}

// replay answers a repeated keyed write from the page it wrote. It reports
// false when that page no longer exists.
func (s *Server) replay(w http.ResponseWriter, r *http.Request, cred credential, status int, pageID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var slug string
	if err := s.db.QueryRow(`SELECT slug FROM pages WHERE id=?`, pageID).Scan(&slug); err != nil {
		return false
	}
	page, _, err := s.pageResource(slug)
	if err != nil || !cred.reaches(page.Agent) {
		return false
	}
	noteRequest(r, "idempotent replay", slog.String("credential", cred.kind()))
	w.Header().Set("Idempotent-Replayed", "true")
	if status == http.StatusCreated {
		w.Header().Set("Location", page.URL)
	}
	writeAPIJSON(w, status, apiPageWrite{apiPage: page, Warnings: s.writeWarnings(nil)})
	return true
}

// slimReply keeps only what replay needs from a write's answer: the page's
// id, plus its slug and path so an older Cairn reading the row can still
// answer with an address. Nothing of the page's title or request is kept.
func slimReply(body []byte) (id string, slim []byte) {
	var page struct{ ID, Slug, URL string }
	if json.Unmarshal(body, &page) != nil || page.ID == "" {
		return "", nil
	}
	slim, _ = json.Marshal(map[string]string{"id": page.ID, "slug": page.Slug, "url": page.URL})
	return page.ID, slim
}

// slimReplies rewrites replies stored by earlier versions, which kept the
// whole answer, original request included, down to what replay needs. It
// runs at every start and skips rows already done.
func slimReplies(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT scope,key,response FROM idempotency_keys WHERE page_id=''`)
	if err != nil {
		return err
	}
	type row struct {
		scope, key, id string
		slim           []byte
	}
	var found []row
	for rows.Next() {
		var x row
		var body []byte
		if err := rows.Scan(&x.scope, &x.key, &body); err != nil {
			rows.Close()
			return err
		}
		x.id, x.slim = slimReply(body)
		found = append(found, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, x := range found {
		if x.id == "" {
			_, err = tx.Exec(`DELETE FROM idempotency_keys WHERE scope=? AND key=?`, x.scope, x.key)
		} else {
			_, err = tx.Exec(`UPDATE idempotency_keys SET page_id=?,response=? WHERE scope=? AND key=?`, x.id, x.slim, x.scope, x.key)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// purgeReplies drops replies older than the replay window.
func (s *Server) purgeReplies() error {
	cutoff := time.Now().Add(-idempotencyLifetime).UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`DELETE FROM idempotency_keys WHERE created_at<?`, cutoff)
	return err
}

// startPurging purges expired replies every hour until Close.
func (s *Server) startPurging() {
	s.purgeStop = make(chan struct{})
	s.purgeDone = make(chan struct{})
	go func() {
		defer close(s.purgeDone)
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			select {
			case <-s.purgeStop:
				return
			case <-tick.C:
				if err := s.purgeReplies(); err != nil {
					s.log.Warn("could not clear old idempotency replies", slog.String("error", err.Error()))
				}
			}
		}
	}()
}

func (s *Server) stopPurging() {
	if s.purgeStop == nil {
		return
	}
	close(s.purgeStop)
	<-s.purgeDone
	s.purgeStop = nil
}

// capture holds a response so idempotent writes can keep it.
type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) WriteHeader(status int) {
	if !c.wrote {
		c.status, c.wrote = status, true
	}
}
func (c *capture) Write(b []byte) (int, error) {
	c.wrote = true
	return c.body.Write(b)
}

// migrateAPI adds what the API keeps: when each page last changed, a
// case-insensitive slug lookup and the idempotency records.
// nowStamp is how pages record when they changed.
func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

// forgetScope drops every stored reply made with a credential that has just
// been rotated or revoked: nothing can replay them any more.
func forgetScope(tx *sql.Tx, scope string) error {
	_, err := tx.Exec(`DELETE FROM idempotency_keys WHERE scope=?`, scope)
	return err
}

func migrateAPI(db *sql.DB) error {
	if err := addColumn(db, "pages", "updated_at", "TEXT NOT NULL DEFAULT ''", `UPDATE pages SET updated_at=created_at`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS pages_slug_nocase ON pages(lower(slug));
 CREATE INDEX IF NOT EXISTS pages_public_slug_nocase ON pages(lower(public_slug));
 CREATE TABLE IF NOT EXISTS idempotency_keys (scope TEXT NOT NULL, key TEXT NOT NULL, fingerprint TEXT NOT NULL, status INTEGER NOT NULL, response BLOB NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(scope,key))`)
	if err != nil {
		return err
	}
	// Stored replies keep only the page they name (page_id), its status and
	// its address; the body is rebuilt from the page on replay.
	if err := addColumn(db, "idempotency_keys", "page_id", "TEXT NOT NULL DEFAULT ''", ""); err != nil {
		return err
	}
	return slimReplies(db)
}

// Health. The public listener answers with status alone; the private one
// adds the version and whether the database and storage work.

func (s *Server) health(w http.ResponseWriter, r *http.Request, public bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, "Use GET /healthz.", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	database, storage := "ok", "ok"
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM pages LIMIT 1`).Scan(&one); err != nil && !errors.Is(err, sql.ErrNoRows) {
		database = "unavailable"
		failRequest(r, err)
	}
	if err := unix.Access(filepath.Join(s.cfg.DataDir, "objects"), unix.W_OK); err != nil {
		storage = "unavailable"
		failRequest(r, err)
	}
	out := apiHealth{Status: "ok"}
	status := http.StatusOK
	if database != "ok" || storage != "ok" {
		out.Status, status = "unavailable", http.StatusServiceUnavailable
	}
	if !public {
		out.Version, out.Database, out.Storage = s.cfg.Version, database, storage
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAPIJSON(w, status, out)
}

// Request logs: one line for each publish, replace, update, refused
// credential and server error. Lines carry routes, slugs, bylines and
// outcomes, never credentials, bodies, titles or requests.

type requestLog struct {
	event string
	level slog.Level
	attrs []slog.Attr
	err   error
}

type requestLogKey struct{}

// noteRequest names what a request did, for its log line.
func noteRequest(r *http.Request, event string, attrs ...slog.Attr) {
	if entry, ok := r.Context().Value(requestLogKey{}).(*requestLog); ok {
		entry.event = event
		entry.attrs = append(entry.attrs, attrs...)
		if event == "auth failure" {
			entry.level = slog.LevelWarn
		}
	}
}

// failRequest records the cause of a server error, for its log line.
func failRequest(r *http.Request, err error) {
	if entry, ok := r.Context().Value(requestLogKey{}).(*requestLog); ok && entry.err == nil {
		entry.err = err
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests runs next and writes the request's log line, if it has one.
func (s *Server) logRequests(w http.ResponseWriter, r *http.Request, listener string, next func(http.ResponseWriter, *http.Request)) {
	entry := &requestLog{level: slog.LevelInfo}
	started := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	next(sw, r.WithContext(context.WithValue(r.Context(), requestLogKey{}, entry)))
	status := sw.status
	if status == 0 {
		status = http.StatusOK
	}
	event := entry.event
	if event == "" && status < 500 {
		return
	}
	level := entry.level
	if status >= 500 {
		level = slog.LevelError
		if event == "" {
			event = "server error"
		}
	}
	attrs := []slog.Attr{slog.String("listener", listener), slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Int("status", status), slog.Int64("ms", time.Since(started).Milliseconds())}
	if event == "auth failure" {
		attrs = append(attrs, slog.String("remote", r.RemoteAddr))
	}
	attrs = append(attrs, entry.attrs...)
	if entry.err != nil {
		attrs = append(attrs, slog.String("error", entry.err.Error()))
	}
	s.log.LogAttrs(context.Background(), level, event, attrs...)
}

// Self-description: GET /api is a short index; /api/openapi.json is the
// full contract, built from the same structs and limits the handlers use.

func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, "Use GET /api.", false)
		return
	}
	writeAPIJSON(w, http.StatusOK, apiIndexDocument(s.preferences().LinkLength))
}

func (s *Server) apiOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAPIError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed, "Use GET /api/openapi.json.", false)
		return
	}
	writeAPIJSON(w, http.StatusOK, openAPIDocument())
}

// exampleRequest and examplePage are the one worked example the index and
// the OpenAPI document show.
var exampleRequest = map[string]any{
	"title":            "Desk lamps under £100: three worth buying",
	"original_request": "can you find me a decent desk lamp under 100 quid, warm light, no flicker",
	"request_summary":  "Find a warm, flicker-free desk lamp under £100.",
	"request_source":   "Claude Code",
	"template":         "comparison",
	"markdown":         "Three lamps stand out for warm, steady light.\n\n## The shortlist\n\n| Lamp | Price | Colour temperature |\n|---|---:|---:|\n| Anglepoise Type 75 Mini | £95 | 2700 K |\n| BenQ e-Reading | £89 | 2700 to 5700 K |\n| IKEA Tertial | £20 | Bulb dependent |",
}

func examplePage() apiPageWrite {
	private := "https://cairn.example.ts.net/k3T"
	layout := "comparison"
	return apiPageWrite{apiPage: apiPage{ID: "4f9c2d1e8b7a6c5d4e3f2a1b0c9d8e7f", Slug: "k3T", URL: "/k3T", PrivateURL: &private, Title: exampleRequest["title"].(string), Agent: "Research agent", Format: "markdown", Template: &layout, OriginalRequest: exampleRequest["original_request"].(string), RequestSummary: exampleRequest["request_summary"].(string), RequestSource: "Claude Code", Assets: []string{}, CreatedAt: "2026-11-12T09:30:00Z", UpdatedAt: "2026-11-12T09:30:00Z"}, Warnings: []apiWarning{}}
}

func apiLimits(linkLength int) map[string]any {
	return map[string]any{
		"request_bytes":          maxRequestBytes,
		"title_bytes":            maxTitleBytes,
		"agent_bytes":            maxAgentBytes,
		"original_request_bytes": maxOriginalRequestBytes,
		"request_summary_bytes":  maxSummaryBytes,
		"request_summary_words":  maxSummaryWords,
		"request_source_bytes":   maxSourceBytes,
		"html_bytes":             maxContentBytes,
		"markdown_bytes":         maxContentBytes,
		"asset_bytes_each":       maxAssetBytes,
		"asset_count":            maxAssets,
		"slug_chars_max":         maxSlugChars,
		"default_slug_chars":     3,
		"slug_chars":             linkLength,
		"list_limit_max":         maxListLimit,
		"idempotency_hours":      int(idempotencyLifetime.Hours()),
	}
}

func apiIndexDocument(linkLength int) map[string]any {
	return map[string]any{
		"name":    "Cairn publishing API",
		"version": APIVersion,
		"openapi": "/api/openapi.json",
		"docs":    "https://github.com/rowesk/cairn/blob/main/docs/publishing.md",
		"authentication": "Send Authorization: Bearer followed by an agent key from Settings, Authors. " +
			"An agent key signs pages with its own byline, and through this API it reads and changes only those pages; another author's page answers 404 as if it did not exist. " +
			"A key is a byline, not a lock: the library's own pages have no sign-in, so software that can reach this address can do anything the owner can. " +
			"The installation token reaches every page and needs agent in each publication. Browsers cannot use the API.",
		"endpoints": []map[string]string{
			{"method": "GET", "path": "/api", "description": "This index. No key needed."},
			{"method": "GET", "path": "/api/openapi.json", "description": "The full OpenAPI 3.1 contract. No key needed."},
			{"method": "POST", "path": "/api/pages", "description": "Publish a private page from Markdown or HTML."},
			{"method": "GET", "path": "/api/pages", "description": "List your pages, newest first, or search them with q."},
			{"method": "GET", "path": "/api/pages/{slug}", "description": "Read a page with its request and the Markdown or HTML Cairn kept. HEAD checks that it exists."},
			{"method": "PUT", "path": "/api/pages/{slug}", "description": "Replace a page completely. The slug, date and sharing stay."},
			{"method": "PATCH", "path": "/api/pages/{slug}", "description": "Archive or restore a page, or change its title."},
			{"method": "GET", "path": "/healthz", "description": "Whether Cairn is up. No key needed."},
		},
		"limits":      apiLimits(linkLength),
		"error_codes": apiErrorCodes,
		"example": map[string]any{
			"request": map[string]any{
				"method":  "POST",
				"path":    "/api/pages",
				"headers": map[string]string{"Authorization": "Bearer $CAIRN_API_KEY", "Content-Type": "application/json", "Idempotency-Key": "lamps-2026-11-12"},
				"body":    exampleRequest,
			},
			"response": map[string]any{"status": http.StatusCreated, "body": examplePage()},
		},
	}
}
