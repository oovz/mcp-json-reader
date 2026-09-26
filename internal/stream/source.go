package stream

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

type ValidationSummary struct {
	RootKind RootKind
	Records  int64
}

// ValidateSource performs a complete bounded-memory validation using the
// selected framing format.
func ValidateSource(ctx context.Context, source io.Reader, format core.Format, limits core.Limits) (ValidationSummary, *core.AppError) {
	if err := limits.Validate(); err != nil {
		return ValidationSummary{}, err
	}
	scanContext, cancel := context.WithTimeout(ctx, limits.MaxScanTime)
	defer cancel()
	source = CheckedReader(scanContext, source)

	switch format {
	case core.FormatJSON:
		info, err := ValidateDocument(scanContext, source, limits)
		if err != nil {
			return ValidationSummary{}, err
		}
		return ValidationSummary{RootKind: info.RootKind, Records: 1}, nil
	case core.FormatJSONL:
		return validateJSONL(scanContext, source, limits)
	case core.FormatJSONSequence:
		return validateJSONSequence(scanContext, source, limits)
	default:
		return ValidationSummary{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "ValidateSource requires an explicit format"}
	}
}

func validateJSONL(ctx context.Context, source io.Reader, limits core.Limits) (ValidationSummary, *core.AppError) {
	framer := NewJSONLFramer(source, limits)
	var records int64
	for {
		record, nextErr := framer.Next()
		if err := ctx.Err(); err != nil {
			return ValidationSummary{}, core.ContextError(err, limits.MaxScanTime)
		}
		if errors.Is(nextErr, io.EOF) {
			return ValidationSummary{RootKind: RootArray, Records: records}, nil
		}
		if nextErr != nil {
			return ValidationSummary{}, SourceError(nextErr, limits.MaxScanTime)
		}

		_, recordErr := ValidateDocument(ctx, record, limits)
		if recordErr != nil {
			return ValidationSummary{}, RecordError(record, recordErr, core.FormatJSONL)
		}
		records++
	}
}

func RecordError(record *RecordReader, err *core.AppError, format core.Format) *core.AppError {
	if format == core.FormatJSONSequence {
		if err.Code == core.CodeFormatMismatch {
			err.ExpectedFormat, err.LikelyFormats, err.Retry = format, nil, nil
			err.Message = "expected exactly one complete JSON value in the JSON sequence record"
		}
		setRecordIndex(err, record.Index())
		return err
	}
	if err.Code == core.CodeSyntax && record.hasDelimiter && record.BytesRead() == 0 {
		return &core.AppError{
			Code:           core.CodeFormatMismatch,
			Message:        "blank lines are not valid JSONL records",
			ExpectedFormat: core.FormatJSONL,
			Location:       &core.Location{ByteOffset: core.Int64(0), RecordIndex: core.Int64(record.Index())},
		}
	}
	trimmed := strings.TrimSpace(record.Sample())
	if record.hasDelimiter && record.Index() == 0 && err.Code == core.CodeSyntax && (trimmed == "{" || trimmed == "[") {
		return &core.AppError{
			Code:           core.CodeFormatMismatch,
			Message:        "expected one complete JSON value on each non-empty line",
			ExpectedFormat: core.FormatJSONL,
			LikelyFormats:  []core.Format{core.FormatJSON},
			Location:       &core.Location{ByteOffset: core.Int64(0), RecordIndex: core.Int64(record.Index())},
			Retry:          &core.Retry{Format: core.FormatJSON},
		}
	}
	if err.Code == core.CodeFormatMismatch {
		err.ExpectedFormat = core.FormatJSONL
		err.LikelyFormats = nil
		err.Retry = nil
		err.Message = "expected exactly one complete JSON value in the JSONL record"
	}
	setRecordIndex(err, record.Index())
	return err
}

func validateJSONSequence(ctx context.Context, source io.Reader, limits core.Limits) (ValidationSummary, *core.AppError) {
	framer := NewJSONSequenceFramer(source, limits)
	var records int64
	for {
		record, nextErr := framer.Next()
		if err := ctx.Err(); err != nil {
			return ValidationSummary{}, core.ContextError(err, limits.MaxScanTime)
		}
		if errors.Is(nextErr, io.EOF) {
			return ValidationSummary{RootKind: RootArray, Records: records}, nil
		}
		if nextErr != nil {
			return ValidationSummary{}, SourceError(nextErr, limits.MaxScanTime)
		}

		info, recordErr := ValidateDocument(ctx, record, limits)
		if recordErr != nil {
			return ValidationSummary{}, RecordError(record, recordErr, core.FormatJSONSequence)
		}
		if err := CheckRecordTerminator(record, info.RootKind, core.FormatJSONSequence); err != nil {
			return ValidationSummary{}, err
		}
		records++
	}
}

func setRecordIndex(err *core.AppError, index int64) {
	if err.Location == nil {
		err.Location = &core.Location{}
	}
	err.Location.RecordIndex = core.Int64(index)
}

func SourceError(err error, maxScanTime time.Duration) *core.AppError {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return core.ContextError(err, maxScanTime)
	}
	if appErr, ok := errors.AsType[*core.AppError](err); ok {
		return appErr
	}
	return &core.AppError{Code: core.CodeIO, Message: "cannot read framed source", Cause: err}
}

// CheckRecordTerminator implements RFC 7464's truncation check for numbers.
func CheckRecordTerminator(record *RecordReader, kind RootKind, format core.Format) *core.AppError {
	if format == core.FormatJSONSequence && kind == RootNumber && !record.EndsWithJSONWhitespace() {
		return &core.AppError{
			Code: core.CodeSyntax, Message: "a top-level number in a JSON sequence must be followed by JSON whitespace",
			Location: &core.Location{ByteOffset: core.Int64(record.BytesRead()), RecordIndex: core.Int64(record.Index())},
			Hint:     "Terminate the number with LF.", Cause: io.ErrUnexpectedEOF,
		}
	}
	return nil
}
