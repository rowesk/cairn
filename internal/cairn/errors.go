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

// APIError is the single error shape returned by private /api routes.
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Field     string `json:"field,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Stable machine-readable error codes.
const (
	ErrAuthenticationRequired = "authentication_required"
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
	ErrStorageUnavailable     = "storage_unavailable"
	ErrCleanupPending         = "storage_cleanup_pending"
	ErrSharingUnavailable     = "public_sharing_unavailable"
	ErrLinkAllocationFailed   = "link_allocation_failed"
)

func writeAPIError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeAPIErrorField(w, status, code, "", message, retryable)
}

func writeAPIErrorField(w http.ResponseWriter, status int, code, field, message string, retryable bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIError{Code: code, Message: message, Field: field, Retryable: retryable})
}

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
