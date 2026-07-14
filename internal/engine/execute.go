package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
	"github.com/oovz/mcp-json-reader/v2/internal/query"
	"github.com/oovz/mcp-json-reader/v2/internal/stream"
)

type Item struct {
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

type Page struct {
	Items []Item `json:"items"`
	More  bool   `json:"more"`
	Seen  int64  `json:"seen"`
}

type PageOptions struct {
	Skip           int64
	MaxItems       int
	MaxResultBytes int64
}

type capture struct {
	itemIndex int
	buffer    bytes.Buffer
	path      core.Path
	filter    bool
}

type executionState struct {
	plan             *query.Plan
	limits           core.Limits
	skip             int64
	maxItems         int
	maxResultBytes   int64
	resultValueBytes int64
	activeKeyBytes   int64
	page             Page
	active           []*capture
}

func Execute(ctx context.Context, source io.Reader, format core.Format, plan *query.Plan, limits core.Limits, options PageOptions) (Page, *core.AppError) {
	if err := limits.Validate(); err != nil {
		return Page{}, err
	}
	if options.Skip < 0 {
		return Page{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "skip cannot be negative"}
	}
	maxItems := options.MaxItems
	if maxItems == 0 || maxItems > limits.MaxItems {
		maxItems = limits.MaxItems
	}
	maxResultBytes := options.MaxResultBytes
	if maxResultBytes == 0 || maxResultBytes > limits.MaxResultBytes {
		maxResultBytes = limits.MaxResultBytes
	}
	if maxItems <= 0 || maxResultBytes <= 0 {
		return Page{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "page limits must be positive"}
	}
	scanContext, cancel := context.WithTimeout(ctx, limits.MaxScanTime)
	defer cancel()
	state := &executionState{plan: plan, limits: limits, skip: options.Skip, maxItems: maxItems, maxResultBytes: maxResultBytes}
	var executeErr *core.AppError
	switch format {
	case core.FormatJSON:
		executeErr = executeDocument(scanContext, source, state, limits)
	case core.FormatJSONL:
		executeErr = executeJSONL(scanContext, source, state, limits)
	case core.FormatJSONSequence:
		executeErr = executeJSONSequence(scanContext, source, state, limits)
	default:
		executeErr = &core.AppError{Code: core.CodeInvalidArgument, Message: "Execute requires an explicit supported format"}
	}
	if executeErr != nil {
		return Page{}, executeErr
	}
	return state.page, nil
}

func executeDocument(ctx context.Context, source io.Reader, state *executionState, limits core.Limits) *core.AppError {
	decoder := newDecoder(ctx, source, limits)
	if err := walkValue(ctx, decoder, state, nil); err != nil {
		return err
	}
	if state.pageReady() {
		return nil
	}
	if _, err := decoder.Token(); err == nil {
		return &core.AppError{Code: core.CodeFormatMismatch, Message: "expected one JSON document, but found another top-level value", ExpectedFormat: core.FormatJSON, LikelyFormats: []core.Format{core.FormatJSONL}, Retry: &core.Retry{Format: core.FormatJSONL}}
	} else if !errors.Is(err, io.EOF) {
		return decodeError(ctx, decoder, err, "", limits.MaxScanTime)
	}
	return nil
}

func executeJSONL(ctx context.Context, source io.Reader, state *executionState, limits core.Limits) *core.AppError {
	framer := stream.NewJSONLFramer(&checkedReader{ctx: ctx, source: source}, limits)
	return executeVirtualArray(ctx, state, func() (*stream.RecordReader, error) { return framer.Next() }, false, limits)
}

func executeJSONSequence(ctx context.Context, source io.Reader, state *executionState, limits core.Limits) *core.AppError {
	framer := stream.NewJSONSequenceFramer(&checkedReader{ctx: ctx, source: source}, limits)
	return executeVirtualArray(ctx, state, func() (*stream.RecordReader, error) { return framer.Next() }, true, limits)
}

func executeVirtualArray(ctx context.Context, state *executionState, next func() (*stream.RecordReader, error), jsonSequence bool, limits core.Limits) *core.AppError {
	rootCapture, beginErr := state.begin(nil)
	if beginErr != nil {
		return beginErr
	}
	if emitErr := state.emit([]byte{'['}); emitErr != nil {
		return emitErr
	}
	var index int64
	for {
		record, nextErr := next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return errorFrom(nextErr)
		}
		if index > 0 {
			if emitErr := state.emit([]byte{','}); emitErr != nil {
				return emitErr
			}
		}
		decoder := newDecoder(ctx, record, limits)
		if valueErr := walkValue(ctx, decoder, state, core.Path{core.IndexSegment(index)}); valueErr != nil {
			return framedRecordError(record, valueErr, jsonSequence)
		}
		if state.pageReady() {
			return nil
		}
		if _, trailingErr := decoder.Token(); trailingErr == nil {
			format := core.FormatJSONL
			if jsonSequence {
				format = core.FormatJSONSequence
			}
			return &core.AppError{Code: core.CodeFormatMismatch, Message: "expected exactly one JSON value in the record", ExpectedFormat: format, Location: &core.Location{RecordIndex: core.Int64(record.Index())}}
		} else if !errors.Is(trailingErr, io.EOF) {
			return framedRecordError(record, decodeError(ctx, decoder, trailingErr, core.Path{core.IndexSegment(index)}.Pointer(), limits.MaxScanTime), jsonSequence)
		}
		if jsonSequence && record.StartsWithNumber() && !record.EndsWithJSONWhitespace() {
			return &core.AppError{Code: core.CodeSyntax, Message: "a top-level number in a JSON sequence must be followed by JSON whitespace", Location: &core.Location{ByteOffset: core.Int64(record.BytesRead()), RecordIndex: core.Int64(record.Index())}}
		}
		index++
	}
	if emitErr := state.emit([]byte{']'}); emitErr != nil {
		return emitErr
	}
	return state.end(ctx, rootCapture)
}

func newDecoder(ctx context.Context, source io.Reader, limits core.Limits) *json.Decoder {
	guard := stream.NewGuardReader(&checkedReader{ctx: ctx, source: source}, limits)
	decoder := json.NewDecoder(guard)
	decoder.UseNumber()
	return decoder
}

func walkValue(ctx context.Context, decoder *json.Decoder, state *executionState, path core.Path) *core.AppError {
	if err := ctx.Err(); err != nil {
		return scanContextError(err, state.limits.MaxScanTime)
	}
	token, err := decoder.Token()
	if err != nil {
		return decodeError(ctx, decoder, err, path.Pointer(), state.limits.MaxScanTime)
	}
	started, beginErr := state.begin(path)
	if beginErr != nil {
		return beginErr
	}
	if state.pageReady() {
		return nil
	}

	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		encoded, encodeErr := encodeScalar(token)
		if encodeErr != nil {
			return encodeErr
		}
		if emitErr := state.emit(encoded); emitErr != nil {
			return emitErr
		}
		return state.end(ctx, started)
	}

	switch delimiter {
	case '{':
		if emitErr := state.emit([]byte{'{'}); emitErr != nil {
			return emitErr
		}
		names := make(map[string]struct{})
		var keyBytes int64
		defer func() { state.activeKeyBytes -= keyBytes }()
		first := true
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return decodeError(ctx, decoder, keyErr, path.Pointer(), state.limits.MaxScanTime)
			}
			key, ok := keyToken.(string)
			if !ok {
				return &core.AppError{Code: core.CodeSyntax, Message: "object member name must be a string", Location: &core.Location{ByteOffset: core.Int64(decoder.InputOffset()), Path: path.Pointer()}}
			}
			memberPath := path.Append(core.PropertySegment(key))
			nextObjectKeyBytes := keyBytes + int64(len(key))
			if nextObjectKeyBytes > state.limits.MaxObjectKeyBytes {
				return &core.AppError{Code: core.CodeResourceLimit, Message: "max_object_key_bytes exceeded", Location: &core.Location{ByteOffset: core.Int64(decoder.InputOffset()), Path: memberPath.Pointer()}, Limit: &core.LimitDetail{Name: "max_object_key_bytes", Limit: state.limits.MaxObjectKeyBytes, Value: nextObjectKeyBytes}}
			}
			if _, duplicate := names[key]; duplicate {
				return &core.AppError{Code: core.CodeSyntax, Message: "duplicate object member name: " + key, Location: &core.Location{ByteOffset: core.Int64(decoder.InputOffset()), Path: memberPath.Pointer()}}
			}
			nextActiveKeyBytes := state.activeKeyBytes + int64(len(key))
			if nextActiveKeyBytes > state.limits.MaxActiveKeyBytes {
				return &core.AppError{Code: core.CodeResourceLimit, Message: "max_active_key_bytes exceeded", Location: &core.Location{ByteOffset: core.Int64(decoder.InputOffset()), Path: memberPath.Pointer()}, Limit: &core.LimitDetail{Name: "max_active_key_bytes", Limit: state.limits.MaxActiveKeyBytes, Value: nextActiveKeyBytes}}
			}
			names[key] = struct{}{}
			keyBytes = nextObjectKeyBytes
			state.activeKeyBytes = nextActiveKeyBytes
			if !first {
				if emitErr := state.emit([]byte{','}); emitErr != nil {
					return emitErr
				}
			}
			first = false
			encodedKey, _ := json.Marshal(key)
			if emitErr := state.emit(encodedKey); emitErr != nil {
				return emitErr
			}
			if emitErr := state.emit([]byte{':'}); emitErr != nil {
				return emitErr
			}
			if childErr := walkValue(ctx, decoder, state, memberPath); childErr != nil {
				return childErr
			}
			if state.pageReady() {
				return nil
			}
		}
		if _, closeErr := decoder.Token(); closeErr != nil {
			return decodeError(ctx, decoder, closeErr, path.Pointer(), state.limits.MaxScanTime)
		}
		if emitErr := state.emit([]byte{'}'}); emitErr != nil {
			return emitErr
		}
	case '[':
		if emitErr := state.emit([]byte{'['}); emitErr != nil {
			return emitErr
		}
		var index int64
		for decoder.More() {
			if index > 0 {
				if emitErr := state.emit([]byte{','}); emitErr != nil {
					return emitErr
				}
			}
			if childErr := walkValue(ctx, decoder, state, path.Append(core.IndexSegment(index))); childErr != nil {
				return childErr
			}
			if state.pageReady() {
				return nil
			}
			index++
		}
		if _, closeErr := decoder.Token(); closeErr != nil {
			return decodeError(ctx, decoder, closeErr, path.Pointer(), state.limits.MaxScanTime)
		}
		if emitErr := state.emit([]byte{']'}); emitErr != nil {
			return emitErr
		}
	default:
		return &core.AppError{Code: core.CodeSyntax, Message: "unexpected closing delimiter"}
	}
	return state.end(ctx, started)
}

func (state *executionState) begin(path core.Path) (*capture, *core.AppError) {
	if state.plan.HasFilter() {
		if state.page.More || !state.plan.IsFilterCandidate(path) {
			return nil, nil
		}
		started := &capture{itemIndex: -1, path: append(core.Path(nil), path...), filter: true}
		state.active = append(state.active, started)
		return started, nil
	}
	if !state.plan.Matches(path) {
		return nil, nil
	}
	state.page.Seen++
	if state.page.Seen <= state.skip {
		return nil, nil
	}
	if len(state.page.Items) >= state.maxItems {
		state.page.More = true
		return nil, nil
	}
	itemIndex := len(state.page.Items)
	state.page.Items = append(state.page.Items, Item{Path: path.Pointer()})
	started := &capture{itemIndex: itemIndex, path: append(core.Path(nil), path...)}
	state.active = append(state.active, started)
	return started, nil
}

func (state *executionState) pageReady() bool {
	return state.page.More && len(state.active) == 0
}

func (state *executionState) emit(encoded []byte) *core.AppError {
	for _, active := range state.active {
		candidateSize := int64(active.buffer.Len() + len(encoded))
		if candidateSize > state.limits.MaxCandidateBytes {
			return resourceError("max_candidate_bytes", state.limits.MaxCandidateBytes, candidateSize)
		}
		if !active.filter {
			state.resultValueBytes += int64(len(encoded))
			if state.resultValueBytes > state.maxResultBytes {
				return resourceError("max_result_bytes", state.maxResultBytes, state.resultValueBytes)
			}
		}
		_, _ = active.buffer.Write(encoded)
	}
	return nil
}

func (state *executionState) end(ctx context.Context, started *capture) *core.AppError {
	if started == nil {
		return nil
	}
	if len(state.active) == 0 || state.active[len(state.active)-1] != started {
		return &core.AppError{Code: core.CodeInternal, Message: "capture stack is inconsistent"}
	}
	state.active = state.active[:len(state.active)-1]
	if started.filter {
		return state.finishFilterCapture(ctx, started)
	}
	state.page.Items[started.itemIndex].Value = append(json.RawMessage(nil), started.buffer.Bytes()...)
	return nil
}

func (state *executionState) finishFilterCapture(ctx context.Context, captured *capture) *core.AppError {
	decoder := json.NewDecoder(bytes.NewReader(captured.buffer.Bytes()))
	decoder.UseNumber()
	var candidate any
	if err := decoder.Decode(&candidate); err != nil {
		return &core.AppError{Code: core.CodeInternal, Message: "failed to decode a bounded filter candidate"}
	}
	var visitErr *core.AppError
	checkpoint := func() *core.AppError {
		if err := ctx.Err(); err != nil {
			return scanContextError(err, state.limits.MaxScanTime)
		}
		return nil
	}
	err := state.plan.VisitFilterCandidate(candidate, captured.path, checkpoint, func(match query.DOMMatch) bool {
		state.page.Seen++
		if state.page.Seen <= state.skip {
			return true
		}
		if len(state.page.Items) >= state.maxItems {
			state.page.More = true
			return false
		}
		encoded, encodeErr := json.Marshal(match.Value)
		if encodeErr != nil {
			visitErr = &core.AppError{Code: core.CodeInternal, Message: "failed to encode a filtered JSON value"}
			return false
		}
		if int64(len(encoded)) > state.limits.MaxCandidateBytes {
			visitErr = resourceError("max_candidate_bytes", state.limits.MaxCandidateBytes, int64(len(encoded)))
			return false
		}
		state.resultValueBytes += int64(len(encoded))
		if state.resultValueBytes > state.maxResultBytes {
			visitErr = resourceError("max_result_bytes", state.maxResultBytes, state.resultValueBytes)
			return false
		}
		state.page.Items = append(state.page.Items, Item{Path: match.Path.Pointer(), Value: append(json.RawMessage(nil), encoded...)})
		return true
	})
	if err != nil {
		return err
	}
	return visitErr
}

func framedRecordError(record *stream.RecordReader, err *core.AppError, jsonSequence bool) *core.AppError {
	if err.Location == nil {
		err.Location = &core.Location{}
	}
	err.Location.RecordIndex = core.Int64(record.Index())
	if !jsonSequence && record.BytesRead() == 0 {
		return &core.AppError{Code: core.CodeFormatMismatch, Message: "blank lines are not valid JSONL records", ExpectedFormat: core.FormatJSONL, Location: &core.Location{ByteOffset: core.Int64(0), RecordIndex: core.Int64(record.Index())}}
	}
	trimmed := strings.TrimSpace(record.Sample())
	if !jsonSequence && record.Index() == 0 && err.Code == core.CodeSyntax && (trimmed == "{" || trimmed == "[") {
		return &core.AppError{Code: core.CodeFormatMismatch, Message: "expected one complete JSON value on each non-empty line", ExpectedFormat: core.FormatJSONL, LikelyFormats: []core.Format{core.FormatJSON}, Retry: &core.Retry{Format: core.FormatJSON}, Location: &core.Location{ByteOffset: core.Int64(0), RecordIndex: core.Int64(0)}}
	}
	return err
}

func errorFrom(err error) *core.AppError {
	var appErr *core.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	return &core.AppError{Code: core.CodeIO, Message: err.Error()}
}

func encodeScalar(token json.Token) ([]byte, *core.AppError) {
	if number, ok := token.(json.Number); ok {
		return []byte(number.String()), nil
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return nil, &core.AppError{Code: core.CodeInternal, Message: "failed to encode JSON scalar"}
	}
	return encoded, nil
}

func decodeError(ctx context.Context, decoder *json.Decoder, err error, path string, maxScanTime time.Duration) *core.AppError {
	var appErr *core.AppError
	if errors.As(err, &appErr) {
		if appErr.Location != nil && appErr.Location.Path == "" {
			appErr.Location.Path = path
		}
		return appErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return scanContextError(contextErr, maxScanTime)
	}
	location := &core.Location{ByteOffset: core.Int64(decoder.InputOffset()), Path: path}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		location.ByteOffset = core.Int64(syntaxErr.Offset - 1)
	}
	message := err.Error()
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		message = "unexpected end of JSON input"
	}
	result := core.NewSyntaxError(message)
	result.Location = location
	return result
}

func resourceError(name string, limit, value int64) *core.AppError {
	return &core.AppError{Code: core.CodeResourceLimit, Message: name + " exceeded", Limit: &core.LimitDetail{Name: name, Limit: limit, Value: value}}
}

func scanContextError(err error, maxScanTime time.Duration) *core.AppError {
	if errors.Is(err, context.DeadlineExceeded) {
		return &core.AppError{Code: core.CodeResourceLimit, Message: "max_scan_time exceeded", Limit: core.ScanTimeLimitDetail(maxScanTime)}
	}
	return &core.AppError{Code: core.CodeCancelled, Message: "operation cancelled"}
}

type checkedReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *checkedReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}
