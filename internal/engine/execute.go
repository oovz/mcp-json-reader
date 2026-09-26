package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/query"
	"github.com/oovz/mcp-json-reader/v3/internal/stream"
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
	// ReservedBytes accounts for the structured service envelope before capture.
	// The engine additionally accounts for the items array and every item.
	ReservedBytes int64
}

type capture struct {
	buffer bytes.Buffer
	path   core.Path
	filter bool
}

type executionState struct {
	plan           *query.Plan
	limits         core.Limits
	skip           int64
	maxItems       int
	maxResultBytes int64
	resultBytes    int64
	page           Page
	active         *capture
}

func Execute(ctx context.Context, source io.Reader, format core.Format, plan *query.Plan, limits core.Limits, options PageOptions) (Page, *core.AppError) {
	if err := limits.Validate(); err != nil {
		return Page{}, err
	}
	if plan == nil || options.Skip < 0 || options.ReservedBytes < 0 || options.MaxItems < 0 || options.MaxResultBytes < 0 {
		return Page{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "a compiled plan and non-negative page options are required"}
	}
	maxItems := options.MaxItems
	if maxItems == 0 || maxItems > limits.MaxItems {
		maxItems = limits.MaxItems
	}
	maxResultBytes := options.MaxResultBytes
	if maxResultBytes == 0 || maxResultBytes > limits.MaxResultBytes {
		maxResultBytes = limits.MaxResultBytes
	}
	if options.ReservedBytes > maxResultBytes-2 {
		return Page{}, resourceError("max_result_bytes", maxResultBytes, 0)
	}
	ctx, cancel := context.WithTimeout(ctx, limits.MaxScanTime)
	defer cancel()
	state := &executionState{
		plan: plan, limits: limits, skip: options.Skip, maxItems: maxItems,
		maxResultBytes: maxResultBytes, resultBytes: options.ReservedBytes + 2,
		page: Page{Items: []Item{}},
	}
	var executeErr *core.AppError
	switch format {
	case core.FormatJSON:
		walker := stream.NewWalker(ctx, source, limits, state)
		if _, executeErr = walker.Walk(nil); executeErr == nil && !state.Done() {
			executeErr = walker.Finish()
		}
	case core.FormatJSONL:
		framer := stream.NewJSONLFramer(stream.CheckedReader(ctx, source), limits)
		executeErr = executeVirtualArray(ctx, state, framer.Next, format)
	case core.FormatJSONSequence:
		framer := stream.NewJSONSequenceFramer(stream.CheckedReader(ctx, source), limits)
		executeErr = executeVirtualArray(ctx, state, framer.Next, format)
	default:
		executeErr = &core.AppError{Code: core.CodeInvalidArgument, Message: "Execute requires an explicit supported format"}
	}
	if executeErr != nil {
		return Page{}, executeErr
	}
	if err := ctx.Err(); err != nil {
		return Page{}, core.ContextError(err, limits.MaxScanTime)
	}
	return state.page, nil
}

func executeVirtualArray(ctx context.Context, state *executionState, next func() (*stream.RecordReader, error), format core.Format) *core.AppError {
	if err := state.Begin(nil); err != nil {
		return err
	}
	if err := state.Token(json.Delim('[')); err != nil {
		return err
	}
	var index int64
	for {
		if err := ctx.Err(); err != nil {
			return core.ContextError(err, state.limits.MaxScanTime)
		}
		record, err := next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stream.SourceError(err, state.limits.MaxScanTime)
		}
		if index > 0 {
			if err := state.Token(json.Delim(',')); err != nil {
				return err
			}
		}
		walker := stream.NewWalker(ctx, record, state.limits, state)
		kind, walkErr := walker.Walk(core.Path{core.IndexSegment(index)})
		if walkErr != nil {
			return stream.RecordError(record, walkErr, format)
		}
		if state.Done() {
			return nil
		}
		if err := walker.Finish(); err != nil {
			return stream.RecordError(record, err, format)
		}
		if err := stream.CheckRecordTerminator(record, kind, format); err != nil {
			return err
		}
		index++
	}
	if err := state.Token(json.Delim(']')); err != nil {
		return err
	}
	return state.End(ctx, nil)
}

// The supported selectors have a fixed depth before the first filter. Captures
// therefore never overlap: the executor needs one active candidate, not a stack.
func (state *executionState) Begin(path core.Path) *core.AppError {
	if state.active != nil || state.page.More {
		return nil
	}
	if state.plan.HasFilter() {
		if state.plan.IsFilterCandidate(path) {
			state.active = &capture{path: path, filter: true}
		}
		return nil
	}
	if !state.plan.Matches(path) || !state.acceptMatch() {
		return nil
	}
	if err := state.reserveItem(path); err != nil {
		return err
	}
	state.page.Items = append(state.page.Items, Item{Path: path.Pointer()})
	state.active = &capture{path: path}
	return nil
}

func (state *executionState) acceptMatch() bool {
	state.page.Seen++
	if state.page.Seen <= state.skip {
		return false
	}
	if len(state.page.Items) >= state.maxItems {
		state.page.More = true
		return false
	}
	return true
}

func (state *executionState) Done() bool { return state.page.More && state.active == nil }

func (state *executionState) reserve(count int64) *core.AppError {
	if count > state.maxResultBytes-state.resultBytes {
		return resourceError("max_result_bytes", state.maxResultBytes, 0)
	}
	state.resultBytes += count
	return nil
}

func (state *executionState) reserveItem(path core.Path) *core.AppError {
	size := int64(len(`{"path":,"value":}`)) + path.PointerJSONSize()
	if len(state.page.Items) > 0 {
		size++
	}
	return state.reserve(size)
}

func (state *executionState) Token(token json.Token) *core.AppError {
	if state.active == nil {
		return nil
	}
	var size int64
	delimiter, punctuation := token.(json.Delim)
	if punctuation {
		size = 1
	} else {
		var err error
		size, err = core.JSONSize(token, math.MaxInt64)
		if err != nil {
			return &core.AppError{Code: core.CodeInternal, Message: "invalid scalar from structural walker", Cause: err}
		}
	}
	if size > state.limits.MaxCandidateBytes-int64(state.active.buffer.Len()) {
		return resourceError("max_candidate_bytes", state.limits.MaxCandidateBytes, 0)
	}
	if !state.active.filter {
		if err := state.reserve(size); err != nil {
			return err
		}
	}
	// Every allocation below has an encoded-size check above it.
	if punctuation {
		_ = state.active.buffer.WriteByte(byte(delimiter))
	} else if number, ok := token.(json.Number); ok {
		_, _ = state.active.buffer.WriteString(string(number))
	} else {
		encoded, err := json.Marshal(token)
		if err != nil {
			return &core.AppError{Code: core.CodeInternal, Message: "cannot encode JSON scalar", Cause: err}
		}
		_, _ = state.active.buffer.Write(encoded)
	}
	return nil
}

func (state *executionState) End(ctx context.Context, path core.Path) *core.AppError {
	if state.active == nil || len(state.active.path) != len(path) {
		return nil
	}
	captured := state.active
	state.active = nil
	if captured.filter {
		return state.finishFilterCapture(ctx, captured)
	}
	// Transfer ownership of the buffer. No writer retains it after this point.
	state.page.Items[len(state.page.Items)-1].Value = captured.buffer.Bytes()
	return nil
}

func (state *executionState) finishFilterCapture(ctx context.Context, captured *capture) *core.AppError {
	checkpoint := func() *core.AppError {
		if err := ctx.Err(); err != nil {
			return core.ContextError(err, state.limits.MaxScanTime)
		}
		return nil
	}
	if err := checkpoint(); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(captured.buffer.Bytes()))
	decoder.UseNumber()
	var candidate any
	if err := decoder.Decode(&candidate); err != nil {
		return &core.AppError{Code: core.CodeInternal, Message: "cannot decode validated filter candidate", Cause: err}
	}
	var visitErr *core.AppError
	err := state.plan.VisitFilterCandidate(candidate, captured.path, checkpoint, func(match query.DOMMatch) bool {
		if !state.acceptMatch() {
			return !state.page.More
		}
		if visitErr = state.reserveItem(match.Path); visitErr != nil {
			return false
		}
		budget := state.maxResultBytes - state.resultBytes
		limitName := "max_result_bytes"
		limit := state.maxResultBytes
		if state.limits.MaxCandidateBytes < budget {
			budget, limit, limitName = state.limits.MaxCandidateBytes, state.limits.MaxCandidateBytes, "max_candidate_bytes"
		}
		size, sizeErr := core.JSONSize(match.Value, budget)
		if sizeErr != nil {
			if errors.Is(sizeErr, core.ErrJSONSize) {
				visitErr = resourceError(limitName, limit, 0)
			} else {
				visitErr = &core.AppError{Code: core.CodeInternal, Message: "cannot measure filtered value", Cause: sizeErr}
			}
			return false
		}
		if visitErr = state.reserve(size); visitErr != nil {
			return false
		}
		encoded, encodeErr := json.Marshal(match.Value)
		if encodeErr != nil {
			visitErr = &core.AppError{Code: core.CodeInternal, Message: "cannot encode filtered value", Cause: encodeErr}
			return false
		}
		state.page.Items = append(state.page.Items, Item{Path: match.Path.Pointer(), Value: encoded})
		return true
	})
	if err != nil {
		return err
	}
	return visitErr
}

func resourceError(name string, limit, value int64) *core.AppError {
	return &core.AppError{Code: core.CodeResourceLimit, Message: name + " exceeded", Limit: &core.LimitDetail{Name: name, Limit: limit, Value: value}}
}
