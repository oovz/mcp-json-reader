package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/engine"
	"github.com/oovz/mcp-json-reader/v3/internal/source"
)

func TestReadResultSizeMatchesWireData(t *testing.T) {
	for _, output := range []ReadOutput{
		{Language: core.QueryPointer, Items: []engine.Item{}, Complete: true},
		{FileID: "jf_123", Language: core.QueryJSONPath, Query: "$.<\\a", Items: []engine.Item{{Path: "/<&~\n😀", Value: json.RawMessage(`{"id":123}`)}}, NextCursor: "jc_123", Stats: ReadStats{Returned: 1, Skipped: 999}},
	} {
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		if got := readResultSize(output); got != int64(len(encoded)) {
			t.Fatalf("size=%d want=%d encoded=%s", got, len(encoded), encoded)
		}
	}
}

func TestServiceDeadlineIncludesScanAdmission(t *testing.T) {
	limits := core.DefaultLimits()
	limits.MaxScanTime = 20 * time.Millisecond
	limits.MaxConcurrentScans = 1
	manager, err := source.NewManager(t.TempDir(), limits, source.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	service := New(manager, limits, Options{})
	service.scanSlots <- struct{}{}
	defer func() { <-service.scanSlots }()
	start := time.Now()
	_, appErr := service.Open(context.Background(), OpenInput{Path: "unread.json"})
	if appErr == nil || appErr.Code != core.CodeResourceLimit {
		t.Fatalf("error=%v", appErr)
	}
	if time.Since(start) > time.Second {
		t.Fatal("admission exceeded deadline")
	}
}

func TestReadRespectsCompleteStructuredResultBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`[{"s":"<&"},{"s":"second"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	manager, err := source.NewManager(root, limits, source.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	service := New(manager, limits, Options{})
	output, appErr := service.Read(context.Background(), ReadInput{Path: "data.json", Language: core.QueryJSONPath, Query: "$[*]", MaxItems: 1, MaxResultBytes: 512})
	if appErr != nil {
		t.Fatal(appErr)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(encoded)) > 512 {
		t.Fatalf("structured result budget exceeded: %d", len(encoded))
	}
}
