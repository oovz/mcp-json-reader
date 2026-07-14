package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
	"github.com/oovz/mcp-json-reader/v2/internal/engine"
	"github.com/oovz/mcp-json-reader/v2/internal/query"
	"github.com/oovz/mcp-json-reader/v2/internal/source"
)

type Options struct {
	Now func() time.Time
}

type OpenInput struct {
	Path       string              `json:"path"`
	Format     core.Format         `json:"format,omitempty"`
	Validation core.ValidationMode `json:"validation,omitempty"`
}

type OpenOutput = source.OpenInfo

type ReadInput struct {
	FileID         string             `json:"file_id,omitempty"`
	Path           string             `json:"path,omitempty"`
	Format         core.Format        `json:"format,omitempty"`
	Language       core.QueryLanguage `json:"language,omitempty"`
	Query          string             `json:"query,omitempty"`
	Cursor         string             `json:"cursor,omitempty"`
	MaxItems       int                `json:"max_items,omitempty"`
	MaxResultBytes int64              `json:"max_result_bytes,omitempty"`
}

type ReadStats struct {
	Returned int   `json:"returned"`
	Skipped  int64 `json:"skipped"`
}

type ReadOutput struct {
	FileID     string             `json:"file_id,omitempty"`
	Language   core.QueryLanguage `json:"language"`
	Query      string             `json:"query"`
	Items      []engine.Item      `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
	Complete   bool               `json:"complete"`
	Stats      ReadStats          `json:"stats"`
}

type CloseInput struct {
	FileID string `json:"file_id"`
}

type CloseOutput struct {
	FileID string `json:"file_id"`
	Closed bool   `json:"closed"`
}

type cursorState struct {
	id             string
	fileID         string
	plan           *query.Plan
	language       core.QueryLanguage
	expression     string
	skip           int64
	maxItems       int
	maxResultBytes int64
	ephemeral      bool
	expires        time.Time
	inUse          bool
}

type serviceHandleState struct {
	active        int
	closing       bool
	closeComplete bool
	closeDone     chan struct{}
	closed        bool
	closeErr      *core.AppError
}

type Service struct {
	sources *source.Manager
	limits  core.Limits
	now     func() time.Time

	cursorMu  sync.Mutex
	cursors   map[string]*cursorState
	handleMu  sync.Mutex
	handles   map[string]*serviceHandleState
	scanSlots chan struct{}
}

func New(sources *source.Manager, limits core.Limits, options Options) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		sources:   sources,
		limits:    limits,
		now:       now,
		cursors:   make(map[string]*cursorState),
		handles:   make(map[string]*serviceHandleState),
		scanSlots: make(chan struct{}, limits.MaxConcurrentScans),
	}
}

func (service *Service) Open(ctx context.Context, input OpenInput) (OpenOutput, *core.AppError) {
	if pathErr := service.textLimit("max_path_bytes", int64(len(input.Path)), service.limits.MaxPathBytes); pathErr != nil {
		return OpenOutput{}, pathErr
	}
	release, acquireErr := service.acquireScan(ctx)
	if acquireErr != nil {
		return OpenOutput{}, acquireErr
	}
	defer release()
	opened, openErr := service.sources.Open(ctx, source.OpenOptions{Path: input.Path, Format: input.Format, Validation: input.Validation})
	if openErr != nil {
		return OpenOutput{}, openErr
	}
	return opened, nil
}

func (service *Service) Read(ctx context.Context, input ReadInput) (ReadOutput, *core.AppError) {
	if input.Cursor != "" {
		if input.FileID != "" || input.Path != "" || input.Format != "" || input.Language != "" || input.Query != "" || input.MaxItems != 0 || input.MaxResultBytes != 0 {
			return ReadOutput{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "cursor continuation cannot include source, query, or limit fields"}
		}
		return service.continueCursor(ctx, input.Cursor)
	}
	if (input.FileID == "") == (input.Path == "") {
		return ReadOutput{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "provide exactly one of file_id or path"}
	}
	if input.FileID != "" && input.Format != "" {
		return ReadOutput{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "format is only valid with an implicit path source"}
	}
	if input.MaxItems < 0 || input.MaxResultBytes < 0 {
		return ReadOutput{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "read limits cannot be negative"}
	}
	if pathErr := service.textLimit("max_path_bytes", int64(len(input.Path)), service.limits.MaxPathBytes); pathErr != nil {
		return ReadOutput{}, pathErr
	}
	if queryErr := service.textLimit("max_query_bytes", int64(len(input.Query)), service.limits.MaxQueryBytes); queryErr != nil {
		return ReadOutput{}, queryErr
	}
	if input.Language == "" {
		return ReadOutput{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "language is required"}
	}
	if _, languageErr := core.ParseQueryLanguage(string(input.Language)); languageErr != nil {
		return ReadOutput{}, languageErr
	}
	plan, compileErr := query.Compile(input.Language, input.Query)
	if compileErr != nil {
		return ReadOutput{}, compileErr
	}
	releaseScan, scanErr := service.acquireScan(ctx)
	if scanErr != nil {
		return ReadOutput{}, scanErr
	}
	defer releaseScan()

	fileID := input.FileID
	ephemeral := false
	if input.Path != "" {
		format := input.Format
		if format == "" {
			format = core.FormatAuto
		}
		opened, openErr := service.sources.Open(ctx, source.OpenOptions{Path: input.Path, Format: format, Validation: core.ValidationProbe, Ephemeral: true})
		if openErr != nil {
			return ReadOutput{}, openErr
		}
		fileID = opened.FileID
		ephemeral = true
	}
	if operationErr := service.beginHandleOperation(fileID); operationErr != nil {
		if ephemeral {
			_, _ = service.closeManagedHandle(fileID)
		}
		return ReadOutput{}, operationErr
	}
	defer service.endHandleOperation(fileID)
	lease, leaseErr := service.sources.Acquire(fileID)
	if leaseErr != nil {
		if ephemeral {
			_, _ = service.closeManagedHandle(fileID)
		}
		return ReadOutput{}, leaseErr
	}
	if !ephemeral && lease.Ephemeral() {
		lease.Release()
		return ReadOutput{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "an implicitly opened file_id can only be continued with its cursor or explicitly closed"}
	}
	page, executeErr := engine.Execute(ctx, lease.File(), lease.Format(), plan, service.limits, engine.PageOptions{MaxItems: input.MaxItems, MaxResultBytes: input.MaxResultBytes})
	lease.Release()
	if executeErr != nil {
		if ephemeral {
			_, _ = service.closeManagedHandle(fileID)
		}
		return ReadOutput{}, executeErr
	}
	return service.finishPage(fileID, plan, page, 0, input.MaxItems, input.MaxResultBytes, ephemeral)
}

func (service *Service) continueCursor(ctx context.Context, cursorID string) (ReadOutput, *core.AppError) {
	releaseScan, scanErr := service.acquireScan(ctx)
	if scanErr != nil {
		return ReadOutput{}, scanErr
	}
	defer releaseScan()
	state, cursorErr := service.lockCursor(cursorID)
	if cursorErr != nil {
		return ReadOutput{}, cursorErr
	}
	if operationErr := service.beginHandleOperation(state.fileID); operationErr != nil {
		service.unlockCursor(state, false)
		if state.ephemeral {
			_, _ = service.closeManagedHandle(state.fileID)
		}
		return ReadOutput{}, operationErr
	}
	defer service.endHandleOperation(state.fileID)
	lease, leaseErr := service.sources.Acquire(state.fileID)
	if leaseErr != nil {
		service.unlockCursor(state, false)
		if state.ephemeral {
			_, _ = service.closeManagedHandle(state.fileID)
		}
		return ReadOutput{}, leaseErr
	}
	page, executeErr := engine.Execute(ctx, lease.File(), lease.Format(), state.plan, service.limits, engine.PageOptions{Skip: state.skip, MaxItems: state.maxItems, MaxResultBytes: state.maxResultBytes})
	lease.Release()
	if executeErr != nil {
		keep := executeErr.Code == core.CodeCancelled
		service.unlockCursor(state, keep)
		if !keep && state.ephemeral {
			_, _ = service.closeManagedHandle(state.fileID)
		}
		return ReadOutput{}, executeErr
	}
	service.unlockCursor(state, false)
	return service.finishPage(state.fileID, state.plan, page, state.skip, state.maxItems, state.maxResultBytes, state.ephemeral)
}

func (service *Service) finishPage(fileID string, plan *query.Plan, page engine.Page, skip int64, maxItems int, maxResultBytes int64, ephemeral bool) (ReadOutput, *core.AppError) {
	items := page.Items
	if items == nil {
		items = []engine.Item{}
	}
	output := ReadOutput{
		FileID:   fileID,
		Language: plan.Language(),
		Query:    plan.Expression(),
		Items:    items,
		Complete: !page.More,
		Stats:    ReadStats{Returned: len(page.Items), Skipped: skip},
	}
	if page.More {
		cursorID, err := service.newCursor(&cursorState{
			fileID:         fileID,
			plan:           plan,
			language:       plan.Language(),
			expression:     plan.Expression(),
			skip:           skip + int64(len(page.Items)),
			maxItems:       maxItems,
			maxResultBytes: maxResultBytes,
			ephemeral:      ephemeral,
		})
		if err != nil {
			if ephemeral {
				_, _ = service.closeManagedHandle(fileID)
			}
			return ReadOutput{}, err
		}
		output.NextCursor = cursorID
		if sizeErr := service.checkResultSize(output, maxResultBytes); sizeErr != nil {
			service.cursorMu.Lock()
			delete(service.cursors, cursorID)
			service.cursorMu.Unlock()
			if ephemeral {
				_, _ = service.closeManagedHandle(fileID)
			}
			return ReadOutput{}, sizeErr
		}
		return output, nil
	}
	if ephemeral {
		output.FileID = ""
	}
	if sizeErr := service.checkResultSize(output, maxResultBytes); sizeErr != nil {
		if ephemeral {
			_, _ = service.closeManagedHandle(fileID)
		}
		return ReadOutput{}, sizeErr
	}
	if ephemeral {
		_, closeErr := service.closeManagedHandle(fileID)
		if closeErr != nil {
			return ReadOutput{}, closeErr
		}
	}
	return output, nil
}

func (service *Service) checkResultSize(output ReadOutput, requested int64) *core.AppError {
	limit := requested
	if limit == 0 || limit > service.limits.MaxResultBytes {
		limit = service.limits.MaxResultBytes
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return &core.AppError{Code: core.CodeInternal, Message: "cannot encode read result"}
	}
	if int64(len(encoded)) > limit {
		return &core.AppError{Code: core.CodeResourceLimit, Message: "max_result_bytes exceeded", Limit: &core.LimitDetail{Name: "max_result_bytes", Limit: limit, Value: int64(len(encoded))}}
	}
	return nil
}

func (service *Service) newCursor(state *cursorState) (string, *core.AppError) {
	id, err := randomID("jc_")
	if err != nil {
		return "", &core.AppError{Code: core.CodeInternal, Message: "cannot allocate a pagination cursor"}
	}
	state.id = id
	state.expires = service.now().Add(service.limits.CursorTTL)
	service.cleanupCursors()
	service.handleMu.Lock()
	handleState := service.handles[state.fileID]
	if handleState == nil || handleState.closing || handleState.active == 0 {
		service.handleMu.Unlock()
		return "", &core.AppError{Code: core.CodeHandleExpired, Message: "file handle is closing or no longer available"}
	}
	service.cursorMu.Lock()
	if len(service.cursors) >= service.limits.MaxCursors {
		value := int64(len(service.cursors) + 1)
		service.cursorMu.Unlock()
		service.handleMu.Unlock()
		return "", &core.AppError{Code: core.CodeResourceLimit, Message: "max_cursors exceeded", Limit: &core.LimitDetail{Name: "max_cursors", Limit: int64(service.limits.MaxCursors), Value: value}}
	}
	service.cursors[id] = state
	service.cursorMu.Unlock()
	service.handleMu.Unlock()
	return id, nil
}

func (service *Service) cleanupCursors() {
	now := service.now()
	service.cursorMu.Lock()
	var ephemeralFiles []string
	for id, state := range service.cursors {
		if !now.Before(state.expires) && !state.inUse {
			delete(service.cursors, id)
			if state.ephemeral {
				ephemeralFiles = append(ephemeralFiles, state.fileID)
			}
		}
	}
	service.cursorMu.Unlock()
	for _, fileID := range ephemeralFiles {
		_, _ = service.closeManagedHandle(fileID)
	}
}

func (service *Service) acquireScan(ctx context.Context) (func(), *core.AppError) {
	select {
	case service.scanSlots <- struct{}{}:
		return func() { <-service.scanSlots }, nil
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return nil, &core.AppError{Code: core.CodeResourceLimit, Message: "max_scan_time exceeded", Limit: core.ScanTimeLimitDetail(service.limits.MaxScanTime)}
		}
		return nil, &core.AppError{Code: core.CodeCancelled, Message: "operation cancelled"}
	}
}

func (service *Service) textLimit(name string, value, limit int64) *core.AppError {
	if value <= limit {
		return nil
	}
	return &core.AppError{Code: core.CodeResourceLimit, Message: name + " exceeded", Limit: &core.LimitDetail{Name: name, Limit: limit, Value: value}}
}

func (service *Service) lockCursor(id string) (*cursorState, *core.AppError) {
	service.cursorMu.Lock()
	state := service.cursors[id]
	if state == nil || !service.now().Before(state.expires) {
		if state != nil {
			delete(service.cursors, id)
		}
		service.cursorMu.Unlock()
		if state != nil && state.ephemeral {
			_, _ = service.closeManagedHandle(state.fileID)
		}
		return nil, &core.AppError{Code: core.CodeHandleExpired, Message: "pagination cursor is no longer available"}
	}
	if state.inUse {
		service.cursorMu.Unlock()
		return nil, &core.AppError{Code: core.CodeInvalidArgument, Message: "pagination cursor is already in use"}
	}
	state.inUse = true
	service.cursorMu.Unlock()
	return state, nil
}

func (service *Service) unlockCursor(state *cursorState, keep bool) {
	service.cursorMu.Lock()
	if current := service.cursors[state.id]; current == state {
		if keep {
			state.inUse = false
			state.expires = service.now().Add(service.limits.CursorTTL)
		} else {
			delete(service.cursors, state.id)
		}
	}
	service.cursorMu.Unlock()
}

func (service *Service) Close(ctx context.Context, input CloseInput) (CloseOutput, *core.AppError) {
	if input.FileID == "" {
		return CloseOutput{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "file_id is required"}
	}
	closed, err := service.closeManagedHandleContext(ctx, input.FileID)
	return CloseOutput{FileID: input.FileID, Closed: closed}, err
}

func (service *Service) beginHandleOperation(fileID string) *core.AppError {
	service.handleMu.Lock()
	defer service.handleMu.Unlock()
	state := service.handles[fileID]
	if state == nil {
		state = &serviceHandleState{}
		service.handles[fileID] = state
	}
	if state.closing {
		return &core.AppError{Code: core.CodeHandleExpired, Message: "file handle is closing or no longer available"}
	}
	state.active++
	return nil
}

func (service *Service) endHandleOperation(fileID string) {
	service.handleMu.Lock()
	state := service.handles[fileID]
	if state != nil {
		if state.active > 0 {
			state.active--
		}
		if state.active == 0 && (!state.closing || state.closeComplete) {
			delete(service.handles, fileID)
		}
	}
	service.handleMu.Unlock()
}

// closeManagedHandle marks the service-level state as closing before it
// removes cursors or waits on the source lease. This prevents an in-flight
// read from publishing a new cursor after close has begun.
func (service *Service) closeManagedHandle(fileID string) (bool, *core.AppError) {
	return service.closeManagedHandleContext(context.Background(), fileID)
}

func (service *Service) closeManagedHandleContext(ctx context.Context, fileID string) (bool, *core.AppError) {
	if err := ctx.Err(); err != nil {
		return false, closeContextError(err)
	}
	state, startClose := service.startClosing(fileID)
	if startClose {
		// Closing is monotonic: request cancellation stops waiting but must not
		// make the handle usable again while its active lease drains.
		go service.finishClose(fileID, state)
	}
	select {
	case <-state.closeDone:
		return state.closed, state.closeErr
	case <-ctx.Done():
		return false, closeContextError(ctx.Err())
	}
}

func (service *Service) startClosing(fileID string) (*serviceHandleState, bool) {
	service.handleMu.Lock()
	state := service.handles[fileID]
	if state == nil {
		state = &serviceHandleState{}
		service.handles[fileID] = state
	}
	if state.closing {
		service.handleMu.Unlock()
		return state, false
	}
	state.closing = true
	state.closeDone = make(chan struct{})
	service.cursorMu.Lock()
	for id, cursor := range service.cursors {
		if cursor.fileID == fileID {
			delete(service.cursors, id)
		}
	}
	service.cursorMu.Unlock()
	service.handleMu.Unlock()
	return state, true
}

func (service *Service) finishClose(fileID string, state *serviceHandleState) {
	closed, closeErr := service.sources.CloseHandle(fileID)
	service.handleMu.Lock()
	state.closed = closed
	state.closeErr = closeErr
	state.closeComplete = true
	close(state.closeDone)
	if current := service.handles[fileID]; current == state {
		if state.active == 0 {
			delete(service.handles, fileID)
		}
	}
	service.handleMu.Unlock()
}

func closeContextError(_ error) *core.AppError {
	return &core.AppError{Code: core.CodeCancelled, Message: "operation cancelled"}
}

func randomID(prefix string) (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(bytes[:]), nil
}
