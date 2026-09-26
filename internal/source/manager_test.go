package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

func TestManagerConfinesPathsAndProvidesIdempotentHandleLifecycle(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.json")
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"id":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(`{"outside":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	manager, err := NewManager(root, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })

	if _, openErr := manager.Open(context.Background(), OpenOptions{Path: outside, Format: core.FormatJSON, Validation: core.ValidationFull}); openErr == nil || openErr.Code != core.CodeAccessDenied {
		t.Fatalf("outside Open error = %#v, want ACCESS_DENIED", openErr)
	}
	opened, openErr := manager.Open(context.Background(), OpenOptions{Path: "data.json", Format: core.FormatJSON, Validation: core.ValidationFull})
	if openErr != nil {
		t.Fatalf("Open error: %v", openErr)
	}
	if opened.FileID == "" || opened.DetectedFormat != core.FormatJSON || !opened.Validation.Complete {
		t.Fatalf("Open result = %#v", opened)
	}
	lease, leaseErr := manager.Acquire(context.Background(), opened.FileID)
	if leaseErr != nil {
		t.Fatalf("Acquire error: %v", leaseErr)
	}
	if lease.Format() != core.FormatJSON {
		t.Fatalf("lease format = %q, want json", lease.Format())
	}
	lease.Release()

	closed, closeErr := manager.CloseHandle(opened.FileID)
	if closeErr != nil || !closed {
		t.Fatalf("first CloseHandle = %v, %v, want true nil", closed, closeErr)
	}
	closed, closeErr = manager.CloseHandle(opened.FileID)
	if closeErr != nil || closed {
		t.Fatalf("second CloseHandle = %v, %v, want false nil", closed, closeErr)
	}
	if _, acquireErr := manager.Acquire(context.Background(), opened.FileID); acquireErr == nil || acquireErr.Code != core.CodeHandleExpired {
		t.Fatalf("Acquire closed handle error = %#v, want HANDLE_EXPIRED", acquireErr)
	}
}

func TestProbeRecordCapWithinByteCap(t *testing.T) {
	for _, tc := range []struct {
		name        string
		format      core.Format
		data        string
		cap         int
		wantRecords int64
		wantBytes   int64
		complete    bool
	}{
		{"jsonl below", core.FormatJSONL, "1\n", 2, 1, 2, true},
		{"jsonl at", core.FormatJSONL, "1\n2\n", 2, 2, 4, true},
		{"jsonl above", core.FormatJSONL, "1\n2\n3\n", 1, 1, 2, false},
		{"jsonl final without LF", core.FormatJSONL, "1\n2", 2, 2, 3, true},
		{"jsonl above without LF", core.FormatJSONL, "1\n2", 1, 1, 2, false},
		{"jsonl malformed beyond cap", core.FormatJSONL, "1\ninvalid\n", 1, 1, 2, false},
		{"seq below", core.FormatJSONSequence, "\x1e1\n", 2, 1, 3, true},
		{"seq at", core.FormatJSONSequence, "\x1e1\n\x1e2\n", 2, 2, 6, true},
		{"seq above", core.FormatJSONSequence, "\x1e1\n\x1e2\n\x1e3\n", 1, 1, 3, false},
		{"seq object without LF", core.FormatJSONSequence, "\x1e{}", 1, 1, 3, true},
		{"seq repeated separators", core.FormatJSONSequence, "\x1e\x1e1\n\x1e\x1e", 1, 1, 6, true},
		{"seq malformed beyond cap", core.FormatJSONSequence, "\x1e1\n\x1einvalid", 1, 1, 3, false},
		{"seq empty", core.FormatJSONSequence, "", 1, 0, 0, true},
		{"seq separators only", core.FormatJSONSequence, "\x1e\x1e", 1, 0, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "data"), []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			limits := core.DefaultLimits()
			limits.ProbeRecords = tc.cap
			manager, err := NewManager(root, limits, ManagerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown()
			opened, appErr := manager.Open(context.Background(), OpenOptions{Path: "data", Format: tc.format, Validation: core.ValidationProbe})
			if appErr != nil {
				t.Fatal(appErr)
			}
			got := opened.Validation
			if got.RecordsExamined != tc.wantRecords || got.BytesExamined != tc.wantBytes || got.Complete != tc.complete {
				t.Fatalf("probe=%+v, want records=%d bytes=%d complete=%v", got, tc.wantRecords, tc.wantBytes, tc.complete)
			}
			if !strings.Contains(tc.name, "malformed") {
				full, err := manager.Open(context.Background(), OpenOptions{Path: "data", Format: tc.format, Validation: core.ValidationFull})
				if err != nil || !full.Validation.Complete || full.Validation.BytesExamined != int64(len(tc.data)) {
					t.Fatalf("full=%+v, error=%v", full, err)
				}
			}
		})
	}
}

func TestManagerClassifiesRejectedAndInvalidSources(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(root, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })

	tests := []struct {
		name     string
		path     string
		wantCode core.ErrorCode
	}{
		{name: "nested traversal", path: "nested/../../outside.json", wantCode: core.CodeAccessDenied},
		{name: "directory", path: "directory", wantCode: core.CodeInvalidArgument},
		{name: "missing file", path: "missing.json", wantCode: core.CodeIO},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, openErr := manager.Open(context.Background(), OpenOptions{Path: test.path, Format: core.FormatJSON})
			if openErr == nil || openErr.Code != test.wantCode {
				t.Fatalf("Open error = %#v, want %s", openErr, test.wantCode)
			}
		})
	}
}

func TestManagerRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.json")
	if err := os.WriteFile(outsideFile, []byte(`{"outside":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "file-link.json")); err != nil {
		t.Skipf("creating symlinks is unavailable in this environment: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "directory-link")); err != nil {
		t.Skipf("creating directory symlinks is unavailable in this environment: %v", err)
	}

	manager, err := NewManager(root, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	for _, path := range []string{"file-link.json", "directory-link/outside.json"} {
		_, openErr := manager.Open(context.Background(), OpenOptions{Path: path, Format: core.FormatJSON})
		if openErr == nil || openErr.Code != core.CodeAccessDenied {
			t.Fatalf("Open(%q) error = %#v, want ACCESS_DENIED", path, openErr)
		}
	}
}

func TestManagerDetectsSourceChangesAfterOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data.json")
	if err := os.WriteFile(path, []byte(`{"id":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(root, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	opened, openErr := manager.Open(context.Background(), OpenOptions{Path: "data.json", Format: core.FormatJSON, Validation: core.ValidationProbe})
	if openErr != nil {
		t.Fatal(openErr)
	}
	if err := os.WriteFile(path, []byte(`{"id":12345}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, acquireErr := manager.Acquire(context.Background(), opened.FileID); acquireErr == nil || acquireErr.Code != core.CodeSourceChanged {
		t.Fatalf("Acquire error = %#v, want SOURCE_CHANGED", acquireErr)
	}
}

func TestManagerExpiresHandlesAndEnforcesOpenLimit(t *testing.T) {
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
	manager, err := NewManager(root, limits, ManagerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	first, openErr := manager.Open(context.Background(), OpenOptions{Path: "a.json", Format: core.FormatJSON, Validation: core.ValidationProbe})
	if openErr != nil {
		t.Fatal(openErr)
	}
	if _, secondErr := manager.Open(context.Background(), OpenOptions{Path: "b.json", Format: core.FormatJSON, Validation: core.ValidationProbe}); secondErr == nil || secondErr.Code != core.CodeResourceLimit {
		t.Fatalf("second Open error = %#v, want RESOURCE_LIMIT_EXCEEDED", secondErr)
	}
	now = now.Add(2 * time.Minute)
	if _, acquireErr := manager.Acquire(context.Background(), first.FileID); acquireErr == nil || acquireErr.Code != core.CodeHandleExpired {
		t.Fatalf("expired Acquire error = %#v, want HANDLE_EXPIRED", acquireErr)
	}
	if _, reopenErr := manager.Open(context.Background(), OpenOptions{Path: "b.json", Format: core.FormatJSON, Validation: core.ValidationProbe}); reopenErr != nil {
		t.Fatalf("Open after expiry error: %v", reopenErr)
	}
}

func TestManagerAutoDetectionIsFixedForHandle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.ndjson"), []byte("{\"id\":1}\n{\"id\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(root, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	opened, openErr := manager.Open(context.Background(), OpenOptions{Path: "data.ndjson", Format: core.FormatAuto, Validation: core.ValidationProbe})
	if openErr != nil {
		t.Fatal(openErr)
	}
	if opened.DetectedFormat != core.FormatJSONL || opened.Detection == nil || opened.Detection.Basis != "extension" {
		t.Fatalf("Open = %#v, want jsonl extension detection", opened)
	}
	lease, leaseErr := manager.Acquire(context.Background(), opened.FileID)
	if leaseErr != nil {
		t.Fatal(leaseErr)
	}
	defer lease.Release()
	if lease.Format() != core.FormatJSONL {
		t.Fatalf("lease format = %q, want fixed jsonl", lease.Format())
	}
}

func TestManagerProbeRejectsDecidableSyntaxErrorsBeforeAnyCompleteRecord(t *testing.T) {
	tests := []struct {
		name    string
		format  core.Format
		content string
	}{
		{name: "json", format: core.FormatJSON, content: `{"bad": @` + strings.Repeat(" ", 64)},
		{name: "jsonl", format: core.FormatJSONL, content: `not-json` + strings.Repeat(" ", 64)},
		{name: "json-seq", format: core.FormatJSONSequence, content: "\x1enot-json" + strings.Repeat(" ", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "data"), []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			limits := core.DefaultLimits()
			limits.ProbeBytes = 16
			manager, err := NewManager(root, limits, ManagerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Shutdown() })
			if _, openErr := manager.Open(context.Background(), OpenOptions{Path: "data", Format: test.format, Validation: core.ValidationProbe}); openErr == nil || openErr.Code != core.CodeSyntax {
				t.Fatalf("Open error = %#v, want SYNTAX_ERROR from decidable prefix", openErr)
			}
		})
	}
}

func TestManagerProbeAcceptsAValidValueTruncatedByProbeBoundary(t *testing.T) {
	root := t.TempDir()
	content := `{"message":"` + strings.Repeat("x", 64) + `"}`
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	limits.ProbeBytes = 16
	manager, err := NewManager(root, limits, ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	opened, openErr := manager.Open(context.Background(), OpenOptions{Path: "data.json", Format: core.FormatJSON, Validation: core.ValidationProbe})
	if openErr != nil {
		t.Fatalf("Open error = %v, want incomplete probe accepted", openErr)
	}
	if opened.Validation.Complete || opened.Validation.BytesExamined != limits.ProbeBytes {
		t.Fatalf("validation = %#v, want incomplete bounded probe", opened.Validation)
	}
}

func TestManagerProbeRejectsIncompleteJSONInDelimiterTerminatedRecords(t *testing.T) {
	tests := []struct {
		name    string
		format  core.Format
		content string
	}{
		{name: "jsonl", format: core.FormatJSONL, content: "{\"x\":\nnull\n" + strings.Repeat(" ", 32)},
		{name: "json-seq", format: core.FormatJSONSequence, content: "\x1e{\"x\":\x1enull\n" + strings.Repeat(" ", 32)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "data"), []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			limits := core.DefaultLimits()
			limits.ProbeBytes = 16
			manager, err := NewManager(root, limits, ManagerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Shutdown() })

			if _, openErr := manager.Open(context.Background(), OpenOptions{Path: "data", Format: test.format, Validation: core.ValidationProbe}); openErr == nil || openErr.Code != core.CodeSyntax {
				t.Fatalf("Open error = %#v, want SYNTAX_ERROR for complete malformed record", openErr)
			}
		})
	}
}

func TestManagerProbeAcceptsFramedRecordTruncatedByProbeBoundary(t *testing.T) {
	tests := []struct {
		name    string
		format  core.Format
		content string
	}{
		{name: "jsonl", format: core.FormatJSONL, content: "null\n{\"message\":\"" + strings.Repeat("x", 64) + "\"}"},
		{name: "json-seq", format: core.FormatJSONSequence, content: "\x1enull\n\x1e{\"message\":\"" + strings.Repeat("x", 64) + "\"}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "data"), []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			limits := core.DefaultLimits()
			limits.ProbeBytes = 16
			manager, err := NewManager(root, limits, ManagerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Shutdown() })

			opened, openErr := manager.Open(context.Background(), OpenOptions{Path: "data", Format: test.format, Validation: core.ValidationProbe})
			if openErr != nil {
				t.Fatalf("Open error = %v, want truncated trailing record accepted", openErr)
			}
			if opened.Validation.Complete || opened.Validation.RecordsExamined != 1 {
				t.Fatalf("validation = %#v, want one complete record plus a truncated trailing record", opened.Validation)
			}
		})
	}
}

func TestManagerProbeAcceptsFirstJSONLRecordTruncatedByProbeBoundary(t *testing.T) {
	for _, probeBytes := range []int64{16, core.DefaultLimits().ProbeBytes} {
		for _, delimiters := range [][2]string{{"{", `"x":1}`}, {"[", "1]"}} {
			root := t.TempDir()
			content := delimiters[0] + strings.Repeat(" ", int(probeBytes)) + delimiters[1] + "\n"
			if err := os.WriteFile(filepath.Join(root, "data.jsonl"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			limits := core.DefaultLimits()
			limits.ProbeBytes = probeBytes
			manager, err := NewManager(root, limits, ManagerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Shutdown() })
			for _, mode := range []core.ValidationMode{core.ValidationFull, core.ValidationProbe} {
				opened, openErr := manager.Open(t.Context(), OpenOptions{Path: "data.jsonl", Format: core.FormatJSONL, Validation: mode})
				if openErr != nil {
					t.Fatalf("probe=%d container=%q mode=%s: %v", probeBytes, delimiters[0], mode, openErr)
				}
				if mode == core.ValidationProbe && (opened.Validation.Complete || opened.Validation.RecordsExamined != 0 || opened.Validation.BytesExamined != probeBytes) {
					t.Fatalf("first record probe=%#v", opened.Validation)
				}
			}
		}
	}
}

func TestManagerProbeDoesNotDiscardDefinitelyInvalidUTF8AtBoundary(t *testing.T) {
	root := t.TempDir()
	content := append([]byte(strings.Repeat(" ", 15)), 0xff)
	content = append(content, []byte(`null`+strings.Repeat(" ", 32))...)
	if err := os.WriteFile(filepath.Join(root, "data.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	limits.ProbeBytes = 16
	manager, err := NewManager(root, limits, ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	if _, openErr := manager.Open(context.Background(), OpenOptions{Path: "data.json", Format: core.FormatJSON, Validation: core.ValidationProbe}); openErr == nil || openErr.Code != core.CodeSyntax {
		t.Fatalf("Open error = %#v, want SYNTAX_ERROR for invalid UTF-8", openErr)
	}
}

func TestValidUTF8ProbePrefixTrimsOnlyAnIncompleteFinalRune(t *testing.T) {
	base := []byte(`{"value":"`)
	incomplete := append(append([]byte(nil), base...), 0xe2, 0x82)
	if got := validUTF8ProbePrefix(incomplete); string(got) != string(base) {
		t.Fatalf("validUTF8ProbePrefix incomplete = %x, want %x", got, base)
	}
	invalid := append(append([]byte(nil), base...), 0xff)
	if got := validUTF8ProbePrefix(invalid); len(got) != len(invalid) {
		t.Fatalf("validUTF8ProbePrefix invalid length = %d, want %d", len(got), len(invalid))
	}
}
