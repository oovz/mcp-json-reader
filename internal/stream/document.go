package stream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

type RootKind string

const (
	RootObject  RootKind = "object"
	RootArray   RootKind = "array"
	RootString  RootKind = "string"
	RootNumber  RootKind = "number"
	RootBoolean RootKind = "boolean"
	RootNull    RootKind = "null"
)

type DocumentInfo struct {
	RootKind RootKind
}

type validationState struct {
	activeKeyBytes int64
}

// ValidateDocument consumes exactly one standard JSON value without
// materializing the document.
func ValidateDocument(ctx context.Context, source io.Reader, limits core.Limits) (DocumentInfo, *core.AppError) {
	if err := limits.Validate(); err != nil {
		return DocumentInfo{}, err
	}
	scanContext, cancel := context.WithTimeout(ctx, limits.MaxScanTime)
	defer cancel()

	guard := NewGuardReader(&contextReader{ctx: scanContext, source: source}, limits)
	decoder := json.NewDecoder(guard)
	decoder.UseNumber()

	rootKind, appErr := validateValue(scanContext, decoder, guard, &validationState{}, nil)
	if appErr != nil {
		return DocumentInfo{}, appErr
	}

	_, tokenErr := decoder.Token()
	if tokenErr == nil {
		return DocumentInfo{}, &core.AppError{
			Code:           core.CodeFormatMismatch,
			Message:        "expected one JSON document, but found another top-level value",
			ExpectedFormat: core.FormatJSON,
			LikelyFormats:  []core.Format{core.FormatJSONL},
			Location:       locationAtDecoder(decoder, ""),
			Retry:          &core.Retry{Format: core.FormatJSONL},
		}
	}
	if !errors.Is(tokenErr, io.EOF) {
		return DocumentInfo{}, mapDecodeError(scanContext, decoder, tokenErr, "", limits.MaxScanTime)
	}
	return DocumentInfo{RootKind: rootKind}, nil
}

func validateValue(ctx context.Context, decoder *json.Decoder, guard *GuardReader, state *validationState, path core.Path) (RootKind, *core.AppError) {
	if err := ctx.Err(); err != nil {
		return "", contextError(err, guard.limits.MaxScanTime)
	}
	token, err := decoder.Token()
	if err != nil {
		return "", mapDecodeError(ctx, decoder, err, path.Pointer(), guard.limits.MaxScanTime)
	}

	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return scalarRootKind(token), nil
	}

	switch delimiter {
	case '{':
		names := make(map[string]struct{})
		var keyBytes int64
		defer func() { state.activeKeyBytes -= keyBytes }()
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return "", mapDecodeError(ctx, decoder, keyErr, path.Pointer(), guard.limits.MaxScanTime)
			}
			key, ok := keyToken.(string)
			if !ok {
				return "", &core.AppError{Code: core.CodeSyntax, Message: "object member name must be a string", Location: locationAtDecoder(decoder, path.Pointer())}
			}
			memberPath := path.Append(core.PropertySegment(key))
			nextObjectKeyBytes := keyBytes + int64(len(key))
			if nextObjectKeyBytes > guard.limits.MaxObjectKeyBytes {
				return "", &core.AppError{Code: core.CodeResourceLimit, Message: "max_object_key_bytes exceeded", Location: locationAtDecoder(decoder, memberPath.Pointer()), Limit: &core.LimitDetail{Name: "max_object_key_bytes", Limit: guard.limits.MaxObjectKeyBytes, Value: nextObjectKeyBytes}}
			}
			if _, duplicate := names[key]; duplicate {
				return "", &core.AppError{Code: core.CodeSyntax, Message: "duplicate object member name: " + key, Location: locationAtDecoder(decoder, memberPath.Pointer())}
			}
			nextActiveKeyBytes := state.activeKeyBytes + int64(len(key))
			if nextActiveKeyBytes > guard.limits.MaxActiveKeyBytes {
				return "", &core.AppError{Code: core.CodeResourceLimit, Message: "max_active_key_bytes exceeded", Location: locationAtDecoder(decoder, memberPath.Pointer()), Limit: &core.LimitDetail{Name: "max_active_key_bytes", Limit: guard.limits.MaxActiveKeyBytes, Value: nextActiveKeyBytes}}
			}
			names[key] = struct{}{}
			keyBytes = nextObjectKeyBytes
			state.activeKeyBytes = nextActiveKeyBytes
			if _, valueErr := validateValue(ctx, decoder, guard, state, memberPath); valueErr != nil {
				return "", valueErr
			}
		}
		if _, closeErr := decoder.Token(); closeErr != nil {
			return "", mapDecodeError(ctx, decoder, closeErr, path.Pointer(), guard.limits.MaxScanTime)
		}
		return RootObject, nil
	case '[':
		var index int64
		for decoder.More() {
			if _, valueErr := validateValue(ctx, decoder, guard, state, path.Append(core.IndexSegment(index))); valueErr != nil {
				return "", valueErr
			}
			index++
		}
		if _, closeErr := decoder.Token(); closeErr != nil {
			return "", mapDecodeError(ctx, decoder, closeErr, path.Pointer(), guard.limits.MaxScanTime)
		}
		return RootArray, nil
	default:
		return "", &core.AppError{Code: core.CodeSyntax, Message: "unexpected closing delimiter", Location: locationAtDecoder(decoder, path.Pointer())}
	}
}

func scalarRootKind(token json.Token) RootKind {
	switch token.(type) {
	case nil:
		return RootNull
	case string:
		return RootString
	case json.Number:
		return RootNumber
	case bool:
		return RootBoolean
	default:
		return RootNumber
	}
}

func mapDecodeError(ctx context.Context, decoder *json.Decoder, err error, path string, maxScanTime time.Duration) *core.AppError {
	var appErr *core.AppError
	if errors.As(err, &appErr) {
		if appErr.Location != nil && appErr.Location.Path == "" {
			appErr.Location.Path = path
		}
		return appErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return contextError(ctxErr, maxScanTime)
	}

	location := locationAtDecoder(decoder, path)
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		offset := syntaxErr.Offset - 1
		if offset < 0 {
			offset = 0
		}
		location.ByteOffset = core.Int64(offset)
	}
	message := err.Error()
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		message = "unexpected end of JSON input"
	}
	result := core.NewSyntaxError(message)
	result.Location = location
	return result
}

func locationAtDecoder(decoder *json.Decoder, path string) *core.Location {
	return &core.Location{ByteOffset: core.Int64(decoder.InputOffset()), Path: path}
}

func contextError(err error, maxScanTime time.Duration) *core.AppError {
	if errors.Is(err, context.DeadlineExceeded) {
		return &core.AppError{
			Code:    core.CodeResourceLimit,
			Message: "max_scan_time exceeded",
			Limit:   core.ScanTimeLimitDetail(maxScanTime),
		}
	}
	return &core.AppError{Code: core.CodeCancelled, Message: "operation cancelled"}
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := reader.source.Read(buffer)
	if err == nil {
		if contextErr := reader.ctx.Err(); contextErr != nil {
			return count, contextErr
		}
	}
	return count, err
}
