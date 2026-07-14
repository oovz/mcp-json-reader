package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
	"github.com/oovz/mcp-json-reader/v2/internal/source"
)

func TestServiceOpenReadAndIdempotentClose(t *testing.T) {
	service, manager := newTestService(t, map[string]string{
		"data.json": `{"orders":[{"id":1},{"id":2}]}`,
	})
	opened, openErr := service.Open(context.Background(), OpenInput{Path: "data.json", Format: core.FormatJSON, Validation: core.ValidationFull})
	if openErr != nil {
		t.Fatalf("Open error: %v", openErr)
	}
	read, readErr := service.Read(context.Background(), ReadInput{FileID: opened.FileID, Language: core.QueryPointer, Query: "/orders/1/id"})
	if readErr != nil {
		t.Fatalf("Read error: %v", readErr)
	}
	if !read.Complete || len(read.Items) != 1 || read.Items[0].Path != "/orders/1/id" || string(read.Items[0].Value) != "2" {
		t.Fatalf("Read = %#v, want exact id", read)
	}
	closed, closeErr := service.Close(context.Background(), CloseInput{FileID: opened.FileID})
	if closeErr != nil || !closed.Closed {
		t.Fatalf("Close = %#v, %v, want closed", closed, closeErr)
	}
	closed, closeErr = service.Close(context.Background(), CloseInput{FileID: opened.FileID})
	if closeErr != nil || closed.Closed {
		t.Fatalf("second Close = %#v, %v, want idempotent no-op", closed, closeErr)
	}
	if _, acquireErr := manager.Acquire(opened.FileID); acquireErr == nil || acquireErr.Code != core.CodeHandleExpired {
		t.Fatalf("Acquire after close = %#v, want HANDLE_EXPIRED", acquireErr)
	}
}

func TestServicePaginatesWithOpaqueProcessScopedCursor(t *testing.T) {
	service, _ := newTestService(t, map[string]string{
		"data.json": `{"orders":[{"id":1},{"id":2},{"id":3}]}`,
	})
	opened, openErr := service.Open(context.Background(), OpenInput{Path: "data.json", Format: core.FormatJSON})
	if openErr != nil {
		t.Fatal(openErr)
	}
	first, firstErr := service.Read(context.Background(), ReadInput{FileID: opened.FileID, Language: core.QueryJSONPath, Query: "$.orders[*].id", MaxItems: 1})
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if first.Complete || first.NextCursor == "" || len(first.Items) != 1 || string(first.Items[0].Value) != "1" {
		t.Fatalf("first page = %#v", first)
	}
	if len(first.NextCursor) < 16 || first.NextCursor[:3] != "jc_" {
		t.Fatalf("cursor %q is not opaque process-scoped id", first.NextCursor)
	}
	second, secondErr := service.Read(context.Background(), ReadInput{Cursor: first.NextCursor})
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	if second.Complete || second.NextCursor == "" || string(second.Items[0].Value) != "2" {
		t.Fatalf("second page = %#v", second)
	}
	third, thirdErr := service.Read(context.Background(), ReadInput{Cursor: second.NextCursor})
	if thirdErr != nil {
		t.Fatal(thirdErr)
	}
	if !third.Complete || third.NextCursor != "" || string(third.Items[0].Value) != "3" {
		t.Fatalf("third page = %#v", third)
	}
}

func TestServiceImplicitOpenClosesCompletedHandle(t *testing.T) {
	service, manager := newTestService(t, map[string]string{"data.json": `{"id":1}`})
	read, readErr := service.Read(context.Background(), ReadInput{Path: "data.json", Format: core.FormatAuto, Language: core.QueryPointer, Query: "/id"})
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !read.Complete || read.FileID != "" || len(read.Items) != 1 {
		t.Fatalf("implicit Read = %#v, want completed auto-close", read)
	}
	if manager.ActiveHandles() != 0 {
		t.Fatalf("active handles = %d, want implicit handle closed", manager.ActiveHandles())
	}
}

func TestServiceImplicitCursorClosesHandleAfterSourceChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data.json")
	if err := os.WriteFile(path, []byte(`[1,2,3]`), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	manager, err := source.NewManager(root, limits, source.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	service := New(manager, limits, Options{})

	first, firstErr := service.Read(context.Background(), ReadInput{Path: "data.json", Language: core.QueryJSONPath, Query: "$[*]", MaxItems: 1})
	if firstErr != nil || first.NextCursor == "" || manager.ActiveHandles() != 1 {
		t.Fatalf("first implicit page = %#v, %v; active handles = %d", first, firstErr, manager.ActiveHandles())
	}
	if err := os.WriteFile(path, []byte(`[10,20,30,40]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, continueErr := service.Read(context.Background(), ReadInput{Cursor: first.NextCursor}); continueErr == nil || continueErr.Code != core.CodeSourceChanged {
		t.Fatalf("continuation error = %#v, want SOURCE_CHANGED", continueErr)
	}
	if manager.ActiveHandles() != 0 {
		t.Fatalf("active handles = %d, want failed implicit continuation closed", manager.ActiveHandles())
	}
	if _, secondErr := service.Read(context.Background(), ReadInput{Cursor: first.NextCursor}); secondErr == nil || secondErr.Code != core.CodeHandleExpired {
		t.Fatalf("reused failed cursor error = %#v, want HANDLE_EXPIRED", secondErr)
	}
}

func TestServiceClosePreventsAnInFlightReadFromPublishingACursor(t *testing.T) {
	service, manager := newTestService(t, map[string]string{"data.json": `[1,2,3]`})
	opened, openErr := service.Open(context.Background(), OpenInput{Path: "data.json", Format: core.FormatJSON})
	if openErr != nil {
		t.Fatal(openErr)
	}
	if operationErr := service.beginHandleOperation(opened.FileID); operationErr != nil {
		t.Fatal(operationErr)
	}
	lease, leaseErr := manager.Acquire(opened.FileID)
	if leaseErr != nil {
		t.Fatal(leaseErr)
	}
	type closeResult struct {
		output CloseOutput
		err    *core.AppError
	}
	closeDone := make(chan closeResult, 1)
	go func() {
		closed, closeErr := service.Close(context.Background(), CloseInput{FileID: opened.FileID})
		closeDone <- closeResult{output: closed, err: closeErr}
	}()
	deadline := time.Now().Add(time.Second)
	closing := false
	for !closing && time.Now().Before(deadline) {
		service.handleMu.Lock()
		state := service.handles[opened.FileID]
		closing = state != nil && state.closing
		service.handleMu.Unlock()
		if !closing {
			time.Sleep(time.Millisecond)
		}
	}
	if !closing {
		lease.Release()
		t.Fatal("Close did not publish closing state before waiting for the active source lease")
	}
	if _, cursorErr := service.newCursor(&cursorState{fileID: opened.FileID}); cursorErr == nil || cursorErr.Code != core.CodeHandleExpired {
		lease.Release()
		t.Fatalf("newCursor after close began = %#v, want HANDLE_EXPIRED", cursorErr)
	}
	lease.Release()
	result := <-closeDone
	closed, closeErr := result.output, result.err
	if closeErr != nil || !closed.Closed {
		t.Fatalf("Close = %#v, %v", closed, closeErr)
	}
	service.endHandleOperation(opened.FileID)
	service.handleMu.Lock()
	_, retained := service.handles[opened.FileID]
	service.handleMu.Unlock()
	if retained {
		t.Fatal("closed handle state remained after the in-flight operation ended")
	}
}

func TestServiceCloseReturnsOnCancellationWhileLeaseCleanupContinues(t *testing.T) {
	service, manager := newTestService(t, map[string]string{"data.json": `[1,2,3]`})
	opened, openErr := service.Open(context.Background(), OpenInput{Path: "data.json", Format: core.FormatJSON})
	if openErr != nil {
		t.Fatal(openErr)
	}
	if operationErr := service.beginHandleOperation(opened.FileID); operationErr != nil {
		t.Fatal(operationErr)
	}
	lease, leaseErr := manager.Acquire(opened.FileID)
	if leaseErr != nil {
		service.endHandleOperation(opened.FileID)
		t.Fatal(leaseErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	type closeResult struct {
		output CloseOutput
		err    *core.AppError
	}
	closeDone := make(chan closeResult, 1)
	go func() {
		output, err := service.Close(ctx, CloseInput{FileID: opened.FileID})
		closeDone <- closeResult{output: output, err: err}
	}()

	deadline := time.Now().Add(time.Second)
	var cleanupDone <-chan struct{}
	for cleanupDone == nil && time.Now().Before(deadline) {
		service.handleMu.Lock()
		state := service.handles[opened.FileID]
		if state != nil && state.closing {
			cleanupDone = state.closeDone
		}
		service.handleMu.Unlock()
		if cleanupDone == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if cleanupDone == nil {
		lease.Release()
		service.endHandleOperation(opened.FileID)
		t.Fatal("Close did not begin before the deadline")
	}

	cancel()
	select {
	case result := <-closeDone:
		if result.err == nil || result.err.Code != core.CodeCancelled || result.output.FileID != opened.FileID || result.output.Closed {
			t.Fatalf("cancelled Close = %#v, %#v, want CANCELLED with cleanup pending", result.output, result.err)
		}
	case <-time.After(time.Second):
		lease.Release()
		service.endHandleOperation(opened.FileID)
		t.Fatal("Close did not return promptly after cancellation")
	}
	if operationErr := service.beginHandleOperation(opened.FileID); operationErr == nil || operationErr.Code != core.CodeHandleExpired {
		lease.Release()
		service.endHandleOperation(opened.FileID)
		t.Fatalf("beginHandleOperation during cancelled cleanup = %#v, want HANDLE_EXPIRED", operationErr)
	}

	lease.Release()
	service.endHandleOperation(opened.FileID)
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("background handle cleanup did not finish after the lease was released")
	}
	if manager.ActiveHandles() != 0 {
		t.Fatalf("active handles = %d, want background cleanup to finish", manager.ActiveHandles())
	}
}

func TestServiceDoesNotRetainIdleHandleCoordinationState(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.json", "b.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(`null`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	limits := core.DefaultLimits()
	limits.MaxOpenFiles = 1
	limits.HandleTTL = time.Minute
	manager, err := source.NewManager(root, limits, source.ManagerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	service := New(manager, limits, Options{Now: func() time.Time { return now }})
	if _, openErr := service.Open(context.Background(), OpenInput{Path: "a.json", Format: core.FormatJSON}); openErr != nil {
		t.Fatal(openErr)
	}
	now = now.Add(2 * time.Minute)
	if _, openErr := service.Open(context.Background(), OpenInput{Path: "b.json", Format: core.FormatJSON}); openErr != nil {
		t.Fatal(openErr)
	}
	service.handleMu.Lock()
	retained := len(service.handles)
	service.handleMu.Unlock()
	if retained != 0 {
		t.Fatalf("service handle states = %d, want only active/closing operations retained", retained)
	}
}

func TestImplicitPaginationHandleIsOnlyForContinuationOrClose(t *testing.T) {
	service, _ := newTestService(t, map[string]string{"data.json": `[1,2,3]`})
	first, firstErr := service.Read(context.Background(), ReadInput{Path: "data.json", Language: core.QueryJSONPath, Query: "$[*]", MaxItems: 1})
	if firstErr != nil || first.FileID == "" || first.NextCursor == "" {
		t.Fatalf("first implicit page = %#v, %v", first, firstErr)
	}
	if _, reuseErr := service.Read(context.Background(), ReadInput{FileID: first.FileID, Language: core.QueryJSONPath, Query: "$[*]", MaxItems: 1}); reuseErr == nil || reuseErr.Code != core.CodeInvalidArgument {
		t.Fatalf("direct reuse error = %#v, want INVALID_ARGUMENT", reuseErr)
	}
	if _, continueErr := service.Read(context.Background(), ReadInput{Cursor: first.NextCursor}); continueErr != nil {
		t.Fatalf("original continuation after rejected reuse: %v", continueErr)
	}
}

func TestServiceResultLimitIncludesSerializedEnvelope(t *testing.T) {
	service, _ := newTestService(t, map[string]string{"data.json": `{"id":1}`})
	_, err := service.Read(context.Background(), ReadInput{Path: "data.json", Language: core.QueryPointer, Query: "/id", MaxResultBytes: 64})
	if err == nil || err.Code != core.CodeResourceLimit || err.Limit == nil || err.Limit.Name != "max_result_bytes" {
		t.Fatalf("Read error = %#v, want max_result_bytes for the complete envelope", err)
	}
}

func TestServiceBoundsQueryPathAndStoredCursorState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`[1,2,3]`), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	limits.MaxQueryBytes = 4
	limits.MaxPathBytes = 16
	limits.MaxCursors = 1
	manager, err := source.NewManager(root, limits, source.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	service := New(manager, limits, Options{})
	if _, openErr := service.Open(context.Background(), OpenInput{Path: strings.Repeat("x", 17), Format: core.FormatJSON}); openErr == nil || openErr.Code != core.CodeResourceLimit || openErr.Limit.Name != "max_path_bytes" {
		t.Fatalf("long path error = %#v, want max_path_bytes", openErr)
	}
	opened, openErr := service.Open(context.Background(), OpenInput{Path: "data.json", Format: core.FormatJSON})
	if openErr != nil {
		t.Fatal(openErr)
	}
	if _, readErr := service.Read(context.Background(), ReadInput{FileID: opened.FileID, Language: core.QueryJSONPath, Query: "$.toolong"}); readErr == nil || readErr.Code != core.CodeResourceLimit || readErr.Limit.Name != "max_query_bytes" {
		t.Fatalf("long query error = %#v, want max_query_bytes", readErr)
	}
	first, firstErr := service.Read(context.Background(), ReadInput{FileID: opened.FileID, Language: core.QueryJSONPath, Query: "$[*]", MaxItems: 1})
	if firstErr != nil || first.NextCursor == "" {
		t.Fatalf("first cursor page = %#v, %v", first, firstErr)
	}
	if _, secondErr := service.Read(context.Background(), ReadInput{FileID: opened.FileID, Language: core.QueryJSONPath, Query: "$[*]", MaxItems: 1}); secondErr == nil || secondErr.Code != core.CodeResourceLimit || secondErr.Limit.Name != "max_cursors" {
		t.Fatalf("second cursor error = %#v, want max_cursors", secondErr)
	}
}

func TestServiceReportsMalformedDataFoundAfterProbeValidation(t *testing.T) {
	tests := []struct {
		name       string
		format     core.Format
		contents   string
		probeBytes int64
		wantCode   core.ErrorCode
		wantRecord *int64
		wantHint   string
	}{
		{
			name:       "json",
			format:     core.FormatJSON,
			contents:   `{"message":"` + strings.Repeat("x", 64) + `","broken":}`,
			probeBytes: 16,
			wantCode:   core.CodeSyntax,
		},
		{
			name:       "json with comments",
			format:     core.FormatJSON,
			contents:   `{"message":"` + strings.Repeat("x", 64) + `",// comment` + "\n" + `"id":1}`,
			probeBytes: 16,
			wantCode:   core.CodeUnsupportedSyntax,
			wantHint:   "JSONC",
		},
		{
			name:       "jsonl later record",
			format:     core.FormatJSONL,
			contents:   "{\"ok\":1}\n{\"broken\":}\n",
			probeBytes: 9,
			wantCode:   core.CodeSyntax,
			wantRecord: int64Pointer(1),
		},
		{
			name:       "json sequence later record",
			format:     core.FormatJSONSequence,
			contents:   "\x1e{\"ok\":1}\n\x1e{\"broken\":}\n",
			probeBytes: int64(len("\x1e{\"ok\":1}\n\x1e")),
			wantCode:   core.CodeSyntax,
			wantRecord: int64Pointer(1),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "data"), []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			limits := core.DefaultLimits()
			limits.ProbeBytes = test.probeBytes
			limits.ProbeRecords = 1
			manager, err := source.NewManager(root, limits, source.ManagerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Shutdown() })
			service := New(manager, limits, Options{})

			_, readErr := service.Read(context.Background(), ReadInput{
				Path:     "data",
				Format:   test.format,
				Language: core.QueryPointer,
				Query:    "/missing",
			})
			if readErr == nil || readErr.Code != test.wantCode {
				t.Fatalf("Read error = %#v, want %s", readErr, test.wantCode)
			}
			if readErr.Location == nil || readErr.Location.ByteOffset == nil {
				t.Fatalf("Read error location = %#v, want byte offset", readErr.Location)
			}
			if test.wantRecord != nil && (readErr.Location.RecordIndex == nil || *readErr.Location.RecordIndex != *test.wantRecord) {
				t.Fatalf("record index = %#v, want %d", readErr.Location.RecordIndex, *test.wantRecord)
			}
			if test.wantHint != "" && !strings.Contains(readErr.Hint, test.wantHint) {
				t.Fatalf("hint = %q, want it to mention %q", readErr.Hint, test.wantHint)
			}
			if manager.ActiveHandles() != 0 {
				t.Fatalf("active handles = %d, want failed implicit read cleaned up", manager.ActiveHandles())
			}
		})
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func newTestService(t *testing.T, files map[string]string) (*Service, *source.Manager) {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	limits := core.DefaultLimits()
	manager, err := source.NewManager(root, limits, source.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	return New(manager, limits, Options{}), manager
}
