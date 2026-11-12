// Structured API errors: one JSON shape for every /api failure.
//
// Agents branch on Code, never on English text. Every /api response,
// success or failure, uses Content-Type application/json.
package cairn

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// APIError is the single error shape returned by private /api routes. The
// top level carries the category and repeats the first problem's message and
// field, so clients written before Errors existed keep working. Errors lists
// every problem Cairn found in the request.
type APIError struct {
	Code      string       `json:"code" doc:"What kind of failure this is. Branch on this, never on message."`
	Message   string       `json:"message" doc:"The first problem, in plain words."`
	Field     string       `json:"field,omitempty" doc:"The field the first problem concerns, when there is one."`
	Retryable bool         `json:"retryable" doc:"True when sending the same request again later may succeed."`
	Errors    []FieldError `json:"errors,omitempty" doc:"Every problem found, in field order. Fix them all before sending again."`
}

// FieldError is one problem with one part of a request.
type FieldError struct {
	Code    string   `json:"code" doc:"What is wrong with the field."`
	Field   string   `json:"field,omitempty" doc:"The field, such as title, capabilities[1] or assets.chart.png."`
	Message string   `json:"message" doc:"What is wrong and what to do, starting with the field name."`
	Limit   int      `json:"limit,omitempty" doc:"The most the field may hold, in unit."`
	Actual  any      `json:"actual,omitempty" doc:"What was sent: a size in unit, or the value when it is short and safe to repeat."`
	Unit    string   `json:"unit,omitempty" doc:"bytes, words or items."`
	Allowed []string `json:"allowed,omitempty" doc:"The values the field accepts."`
	Line    int      `json:"line,omitempty" doc:"Line of the problem: in the Markdown for charts, in the body for invalid JSON."`
	Column  int      `json:"column,omitempty" doc:"Column of the problem in the body, for invalid JSON."`
	Block   int      `json:"block,omitempty" doc:"Which echarts block, counting from 1."`
}

// Stable machine-readable error codes.
const (
	ErrAuthenticationRequired = "authentication_required"
	ErrInvalidCredential      = "invalid_credential"
	ErrBrowserOriginForbidden = "browser_origin_forbidden"
	ErrForbidden              = "forbidden"
	ErrUnsupportedMediaType   = "unsupported_media_type"
	ErrInvalidJSON            = "invalid_json"
	ErrValidationFailed       = "validation_failed"
	ErrSlugConflict           = "slug_conflict"
	ErrPublicSlugUnavailable  = "public_slug_unavailable"
	ErrNotFound               = "not_found"
	ErrMethodNotAllowed       = "method_not_allowed"
	ErrPayloadTooLarge        = "payload_too_large"
	ErrIdempotencyKeyReused   = "idempotency_key_reused"
	ErrIdempotencyInProgress  = "idempotency_key_in_progress"
	ErrStorageUnavailable     = "storage_unavailable"
	ErrCleanupPending         = "storage_cleanup_pending"
	ErrSharingUnavailable     = "public_sharing_unavailable"
	ErrLinkAllocationFailed   = "link_allocation_failed"
)

// apiErrorCodes lists every top-level code, for the index and OpenAPI.
var apiErrorCodes = []string{
	ErrAuthenticationRequired, ErrInvalidCredential, ErrBrowserOriginForbidden,
	ErrForbidden, ErrUnsupportedMediaType, ErrInvalidJSON, ErrValidationFailed,
	ErrSlugConflict, ErrPublicSlugUnavailable, ErrNotFound, ErrMethodNotAllowed,
	ErrPayloadTooLarge, ErrIdempotencyKeyReused, ErrIdempotencyInProgress,
	ErrStorageUnavailable, ErrCleanupPending, ErrSharingUnavailable,
	ErrLinkAllocationFailed,
}

// Codes for one problem inside errors[].
const (
	FieldRequired         = "required"
	FieldTooLong          = "too_long"
	FieldTooManyWords     = "too_many_words"
	FieldMultipleLines    = "multiple_lines"
	FieldConflict         = "conflict"
	FieldInvalidValue     = "invalid_value"
	FieldUnknown          = "unknown_field"
	FieldWrongType        = "wrong_type"
	FieldInvalidBase64    = "invalid_base64"
	FieldInvalidSlug      = "invalid_slug"
	FieldReservedSlug     = "reserved_slug"
	FieldInvalidAssetName = "invalid_asset_name"
	FieldEmptyAsset       = "empty_asset"
	FieldTooLarge         = "too_large"
	FieldTooManyItems     = "too_many_items"
	FieldUnsupportedImage = "unsupported_image"
	FieldInvalidChart     = "invalid_chart"
	FieldSyntax           = "syntax"
	FieldReadOnly         = "read_only"
	FieldNotAllowed       = "not_allowed"
	FieldSlugTaken        = "slug_taken"
)

var fieldErrorCodes = []string{
	FieldRequired, FieldTooLong, FieldTooManyWords, FieldMultipleLines,
	FieldConflict, FieldInvalidValue, FieldUnknown, FieldWrongType,
	FieldInvalidBase64, FieldInvalidSlug, FieldReservedSlug,
	FieldInvalidAssetName, FieldEmptyAsset, FieldTooLarge, FieldTooManyItems,
	FieldUnsupportedImage, FieldInvalidChart, FieldSyntax, FieldReadOnly,
	FieldNotAllowed, FieldSlugTaken,
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeAPIErrorField(w, status, code, "", message, retryable)
}

func writeAPIErrorField(w http.ResponseWriter, status int, code, field, message string, retryable bool) {
	writeAPIErrorBody(w, status, APIError{Code: code, Message: message, Field: field, Retryable: retryable})
}

// writeAPIProblems answers with every problem found. The first one also
// fills the top-level message and field.
func writeAPIProblems(w http.ResponseWriter, status int, code string, problems []FieldError) {
	e := APIError{Code: code, Errors: problems}
	if len(problems) > 0 {
		e.Message, e.Field = problems[0].Message, problems[0].Field
	}
	writeAPIErrorBody(w, status, e)
}

func writeAPIErrorBody(w http.ResponseWriter, status int, e APIError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = apiEncoder(w).Encode(e)
}

// fieldProblem is a validation failure as a Go error, for callers that only
// need the first message, such as the editor's preview.
type fieldProblem struct{ FieldError }

func (p *fieldProblem) Error() string { return p.Message }

// apiErrorMessage extracts the human message from a recorder/body holding
// either an APIError JSON object or legacy plain text.
func apiErrorMessage(body string) string {
	var apiErr APIError
	if err := json.Unmarshal([]byte(body), &apiErr); err == nil && apiErr.Message != "" {
		return apiErr.Message
	}
	return strings.TrimSpace(body)
}

// isPayloadTooLarge reports whether err wraps http.MaxBytesError, including
// the string form produced when a JSON body exceeds the read limit.
func isPayloadTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return true
	}
	return strings.Contains(err.Error(), "request body too large")
}
