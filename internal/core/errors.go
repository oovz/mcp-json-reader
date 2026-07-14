package core

import (
	"time"
)

// ErrorCode is a stable machine-readable application error code.
type ErrorCode string

const (
	CodeUnsupportedFormat       ErrorCode = "UNSUPPORTED_FORMAT"
	CodeFormatMismatch          ErrorCode = "FORMAT_MISMATCH"
	CodeSyntax                  ErrorCode = "SYNTAX_ERROR"
	CodeUnsupportedSyntax       ErrorCode = "UNSUPPORTED_SYNTAX"
	CodeQuerySyntax             ErrorCode = "QUERY_SYNTAX_ERROR"
	CodeUnsupportedQueryFeature ErrorCode = "UNSUPPORTED_QUERY_FEATURE"
	CodeResourceLimit           ErrorCode = "RESOURCE_LIMIT_EXCEEDED"
	CodeSourceChanged           ErrorCode = "SOURCE_CHANGED"
	CodeHandleExpired           ErrorCode = "HANDLE_EXPIRED"
	CodeInvalidArgument         ErrorCode = "INVALID_ARGUMENT"
	CodeAccessDenied            ErrorCode = "ACCESS_DENIED"
	CodeIO                      ErrorCode = "IO_ERROR"
	CodeCancelled               ErrorCode = "CANCELLED"
	CodeInternal                ErrorCode = "INTERNAL_ERROR"
)

// Location describes a position as far as it is known. Pointer fields keep
// zero values distinguishable from unknown values.
type Location struct {
	ByteOffset  *int64 `json:"byte_offset,omitempty"`
	Line        *int64 `json:"line,omitempty"`
	Column      *int64 `json:"column,omitempty"`
	RecordIndex *int64 `json:"record_index,omitempty"`
	Path        string `json:"path,omitempty"`
}

// Retry is a machine-readable suggestion for a deterministic retry.
type Retry struct {
	Format Format `json:"format,omitempty"`
}

// LimitDetail identifies the resource policy that stopped an operation.
type LimitDetail struct {
	Name  string `json:"name"`
	Limit int64  `json:"limit"`
	Value int64  `json:"value,omitempty"`
}

// AppError is returned for failures that should be represented as MCP tool
// errors rather than JSON-RPC protocol errors.
type AppError struct {
	Code           ErrorCode    `json:"code"`
	Message        string       `json:"message"`
	ExpectedFormat Format       `json:"expected_format,omitempty"`
	LikelyFormats  []Format     `json:"likely_formats,omitempty"`
	Location       *Location    `json:"location,omitempty"`
	Retry          *Retry       `json:"retry,omitempty"`
	Hint           string       `json:"hint,omitempty"`
	Limit          *LimitDetail `json:"limit,omitempty"`
}

func (e *AppError) Error() string { return e.Message }

// NewSyntaxError constructs an error for malformed standard JSON.
func NewSyntaxError(message string) *AppError {
	return &AppError{Code: CodeSyntax, Message: message}
}

// Int64 returns a pointer suitable for an optional numeric response field.
func Int64(value int64) *int64 { return &value }

// ScanTimeLimitDetail reports duration limits in milliseconds.
func ScanTimeLimitDetail(limit time.Duration) *LimitDetail {
	milliseconds := limit.Milliseconds()
	if milliseconds < 1 {
		milliseconds = 1
	}
	return &LimitDetail{Name: "max_scan_time", Limit: milliseconds, Value: milliseconds}
}
