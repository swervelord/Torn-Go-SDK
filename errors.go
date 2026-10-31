package torn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/swervelord/Torn-Go-SDK/internal/contract"
)

// ErrorCode is a Torn API error code (PARITY_SPEC P5.1). Constants such as
// CodeIncorrectKey are generated from spec/shared/error-codes.json; codes the
// SDK does not know yet are still representable.
type ErrorCode int

type errorCodeInfo struct {
	name, snake, schema, message, category, action, disableKey string
	retryable                                                  bool
}

// Error-code actions from spec/shared/error-codes.json (P2.3, P4.1).
const (
	ActionFail           = "fail"
	ActionRotateKey      = "rotate_key"
	ActionWaitRateWindow = "wait_rate_window"
	ActionRotateRoute    = "rotate_route"
	ActionRetryBackoff   = "retry_backoff"
	ActionRerouteVersion = "reroute_version"
)

// IsKnown reports whether the code is in the pinned error-code table.
func (c ErrorCode) IsKnown() bool { _, ok := errorCodeTable[c]; return ok }

// Name returns the code's PascalCase name, or "Unknown<n>" for unknown codes.
func (c ErrorCode) Name() string {
	if i, ok := errorCodeTable[c]; ok {
		return i.name
	}
	return "Unknown" + strconv.Itoa(int(c))
}

// String returns the snake_case name and the number, e.g. "incorrect_key(2)".
func (c ErrorCode) String() string {
	if i, ok := errorCodeTable[c]; ok {
		return i.snake + "(" + strconv.Itoa(int(c)) + ")"
	}
	return "unknown(" + strconv.Itoa(int(c)) + ")"
}

// Message returns Torn's documented message for the code.
func (c ErrorCode) Message() string { return errorCodeTable[c].message }

// Category returns key, rate, ip, transient, request, access, routing or server.
func (c ErrorCode) Category() string { return errorCodeTable[c].category }

// Action returns the client policy for the code (an Action* constant). Unknown
// codes fail.
func (c ErrorCode) Action() string {
	if i, ok := errorCodeTable[c]; ok {
		return i.action
	}
	return ActionFail
}

// DisablesKey returns "none", "client_lifetime" or "until_utc_midnight".
func (c ErrorCode) DisablesKey() string {
	if i, ok := errorCodeTable[c]; ok {
		return i.disableKey
	}
	return "none"
}

// Retryable reports whether the client recovers from the code automatically
// (by rotating keys or routes, waiting, backing off or rerouting).
func (c ErrorCode) Retryable() bool { return errorCodeTable[c].retryable }

// ErrValidation is matched by every client-side validation failure:
// errors.Is(err, ErrValidation) holds for *ValidationError and *AccessError.
var ErrValidation = errors.New("torn: request validation failed")

// APIError is a Torn API error response (P5.2). Message has configured keys
// redacted; KeyHint is "***" plus at most the last four key characters.
type APIError struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	KeyHint   string `json:"key_hint,omitempty"`
	Operation string `json:"operation,omitempty"`
}

func (e *APIError) Error() string { return fmt.Sprintf("Torn API error %d: %s", e.Code, e.Message) }

// TornCode returns the code as an ErrorCode.
func (e *APIError) TornCode() ErrorCode { return ErrorCode(e.Code) }

// HTTPError is a non-2xx HTTP response.
type HTTPError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Torn HTTP status %d", e.StatusCode) }

// TransportError is a network, TLS, timeout or body-read failure. Cause is
// the sanitized underlying error (request URLs are dropped because v1 URLs
// carry keys), so errors.Is(err, context.DeadlineExceeded) works for
// per-attempt timeouts.
type TransportError struct {
	Message string
	Cause   error
}

func (e *TransportError) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return e.Message + ": " + e.Cause.Error()
}

// Unwrap returns the sanitized cause.
func (e *TransportError) Unwrap() error { return e.Cause }

// DecodeError reports a response that does not fit its generated model
// (P1.4). Raw returns the complete response body, so no data is lost.
type DecodeError struct {
	// Operation is the operation ID or "resource/selection" being decoded.
	Operation string
	// Path is the JSON path of the offending value, e.g. $.items[3].type.
	Path string
	// Expected names the expected type, rule or property.
	Expected string
	// Value is a truncated, key-redacted excerpt of the offending value.
	Value string
	// Strict is true when the failure came from opt-in strict validation.
	Strict bool
	// Err is the underlying *models.DecodeError or other cause.
	Err error
	raw []byte
}

func (e *DecodeError) Error() string {
	msg := "torn: decode " + e.Operation + " at " + e.Path + ": expected " + e.Expected
	if e.Value != "" {
		msg += ", got " + e.Value
	}
	return msg
}

// Unwrap returns the underlying cause.
func (e *DecodeError) Unwrap() error { return e.Err }

// Raw returns a copy of the complete response body that failed to decode.
func (e *DecodeError) Raw() []byte { return append([]byte(nil), e.raw...) }

// ValidationError is a client-side rejection made before sending (P5.2).
type ValidationError struct {
	Message string
	Err     error
}

func (e *ValidationError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the underlying cause.
func (e *ValidationError) Unwrap() error { return e.Err }

// Is matches ErrValidation.
func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

func validationf(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

func paramsError(operation string, err error) error {
	return &ValidationError{Message: operation + " parameters", Err: err}
}

// AccessError reports that no usable key has the access level an operation
// requires; it is returned before sending when CheckAccess is enabled (P6.2).
type AccessError struct {
	Operation string
	Required  AccessLevel
	// Have lists the known levels of the keys that were considered.
	Have []AccessLevel
}

func (e *AccessError) Error() string {
	return fmt.Sprintf("torn: %s requires a %s access key; configured keys have %v", e.Operation, e.Required, e.Have)
}

// Is matches ErrValidation.
func (e *AccessError) Is(target error) bool { return target == ErrValidation }

// NoUsableKeysError reports that every configured key is disabled or was
// rejected for this request (P2.4). Last is the final API error.
type NoUsableKeysError struct {
	Last error
}

func (e *NoUsableKeysError) Error() string {
	if e.Last == nil {
		return "torn: no usable API keys"
	}
	return "torn: no usable API keys; last error: " + e.Last.Error()
}

// Unwrap returns the last API error.
func (e *NoUsableKeysError) Unwrap() error { return e.Last }

// Code returns the Torn error code carried by err (also through
// NoUsableKeysError and other wrappers).
func Code(err error) (ErrorCode, bool) {
	var api *APIError
	if errors.As(err, &api) {
		return ErrorCode(api.Code), true
	}
	return 0, false
}

// IsRetryable reports whether retrying the same request later can succeed:
// transient Torn codes, rate limits, retryable HTTP statuses and transport
// failures (P5.3).
func IsRetryable(err error) bool {
	if code, ok := Code(err); ok {
		switch code.Action() {
		case ActionRetryBackoff, ActionWaitRateWindow, ActionRotateRoute:
			return true
		}
		return false
	}
	var h *HTTPError
	if errors.As(err, &h) {
		return retryHTTPStatuses[h.StatusCode]
	}
	var t *TransportError
	return errors.As(err, &t) && !errors.Is(err, context.Canceled)
}

// IsKeyError reports a problem with the key itself (codes 1, 2, 10, 13, 14, 18)
// or that no usable key remains.
func IsKeyError(err error) bool {
	var none *NoUsableKeysError
	if errors.As(err, &none) {
		return true
	}
	code, ok := Code(err)
	return ok && code.Category() == "key"
}

// IsRateLimit reports Torn's per-user rate limit (code 5), an IP block (code 8)
// or HTTP 429.
func IsRateLimit(err error) bool {
	if code, ok := Code(err); ok {
		return code == CodeTooManyRequests || code == CodeIpBlocked
	}
	var h *HTTPError
	return errors.As(err, &h) && h.StatusCode == 429
}

// IsAccessError reports insufficient key access: code 16, code 7 (private
// selection) or a client-side *AccessError.
func IsAccessError(err error) bool {
	var a *AccessError
	if errors.As(err, &a) {
		return true
	}
	code, ok := Code(err)
	return ok && (code == CodeAccessLevelTooLow || code == CodeIncorrectIdEntityRelation)
}

// IsTransient reports temporary upstream or network trouble that backoff
// retries handle (transient codes, retryable HTTP statuses, transport errors).
func IsTransient(err error) bool {
	if code, ok := Code(err); ok {
		return code.Action() == ActionRetryBackoff
	}
	return IsRetryable(err)
}

// IsDecodeError reports a typed decode failure.
func IsDecodeError(err error) bool {
	var d *DecodeError
	return errors.As(err, &d)
}

// newDecodeError wraps a model decode failure with the operation and body.
func (c *Client) newDecodeError(operation string, raw []byte, err error, strict bool) error {
	d := &DecodeError{Operation: operation, Path: "$", Expected: "decodable response", Strict: strict, Err: err, raw: append([]byte(nil), raw...)}
	var de *contract.DecodeError
	if errors.As(err, &de) {
		d.Path, d.Expected, d.Value = de.Path, de.Expected, de.Value
	}
	if c != nil {
		d.Value = c.redact(d.Value)
	}
	return d
}

// redactKey returns "***" plus at most the last four characters of a key.
func redactKey(key string) string {
	if len(key) < 8 {
		return "***"
	}
	return "***" + key[len(key)-4:]
}

func (c *Client) redact(s string) string {
	if c == nil {
		return s
	}
	for _, k := range c.pool.allKeys() {
		if k != "" {
			s = strings.ReplaceAll(s, k, redactKey(k))
		}
	}
	return s
}
