package core

import "time"

// QueryLanguage identifies a supported query syntax.
type QueryLanguage string

const (
	QueryPointer  QueryLanguage = "pointer"
	QueryJSONPath QueryLanguage = "jsonpath"
)

func ParseQueryLanguage(name string) (QueryLanguage, *AppError) {
	language := QueryLanguage(name)
	switch language {
	case QueryPointer, QueryJSONPath:
		return language, nil
	default:
		return "", &AppError{Code: CodeQuerySyntax, Message: "unsupported query language: " + name}
	}
}

// ValidationMode controls how much of a source json_open validates.
type ValidationMode string

const (
	ValidationProbe ValidationMode = "probe"
	ValidationFull  ValidationMode = "full"
)

func ParseValidationMode(name string) (ValidationMode, *AppError) {
	if name == "" {
		return ValidationProbe, nil
	}
	mode := ValidationMode(name)
	switch mode {
	case ValidationProbe, ValidationFull:
		return mode, nil
	default:
		return "", &AppError{Code: CodeInvalidArgument, Message: "unsupported validation mode: " + name}
	}
}

// Limits bounds parser, query, output, and lifecycle resources. Byte limits
// count encoded UTF-8 bytes.
type Limits struct {
	MaxPathBytes       int64
	MaxQueryBytes      int64
	MaxScalarBytes     int64
	MaxNumberBytes     int64
	MaxDepth           int
	MaxObjectMembers   int
	MaxObjectKeyBytes  int64
	MaxActiveKeyBytes  int64
	MaxRecordBytes     int64
	MaxCandidateBytes  int64
	MaxResultBytes     int64
	MaxItems           int
	MaxScanTime        time.Duration
	ProbeBytes         int64
	ProbeRecords       int
	MaxOpenFiles       int
	MaxCursors         int
	MaxConcurrentScans int
	HandleTTL          time.Duration
	CursorTTL          time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxPathBytes:       32 << 10,
		MaxQueryBytes:      16 << 10,
		MaxScalarBytes:     1 << 20,
		MaxNumberBytes:     1 << 10,
		MaxDepth:           256,
		MaxObjectMembers:   100_000,
		MaxObjectKeyBytes:  8 << 20,
		MaxActiveKeyBytes:  32 << 20,
		MaxRecordBytes:     64 << 20,
		MaxCandidateBytes:  8 << 20,
		MaxResultBytes:     4 << 20,
		MaxItems:           1_000,
		MaxScanTime:        30 * time.Second,
		ProbeBytes:         4 << 20,
		ProbeRecords:       32,
		MaxOpenFiles:       32,
		MaxCursors:         128,
		MaxConcurrentScans: 4,
		HandleTTL:          15 * time.Minute,
		CursorTTL:          5 * time.Minute,
	}
}

func (limits Limits) Validate() *AppError {
	if limits.MaxPathBytes <= 0 || limits.MaxQueryBytes <= 0 || limits.MaxScalarBytes <= 0 || limits.MaxNumberBytes <= 0 || limits.MaxDepth <= 0 ||
		limits.MaxObjectMembers <= 0 || limits.MaxObjectKeyBytes <= 0 || limits.MaxActiveKeyBytes <= 0 || limits.MaxRecordBytes <= 0 || limits.MaxCandidateBytes <= 0 ||
		limits.MaxResultBytes <= 0 || limits.MaxItems <= 0 || limits.MaxScanTime <= 0 ||
		limits.ProbeBytes <= 0 || limits.ProbeRecords <= 0 || limits.MaxOpenFiles <= 0 || limits.MaxCursors <= 0 || limits.MaxConcurrentScans <= 0 ||
		limits.HandleTTL <= 0 || limits.CursorTTL <= 0 {
		return &AppError{Code: CodeInvalidArgument, Message: "all resource limits must be positive"}
	}
	if limits.MaxNumberBytes > limits.MaxScalarBytes {
		return &AppError{Code: CodeInvalidArgument, Message: "max_number_bytes cannot exceed max_scalar_bytes"}
	}
	return nil
}
