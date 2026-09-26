package source

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

func TestLeaseDetectsMutationAfterReadingBeforePublication(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data.json")
	if err := os.WriteFile(path, []byte(`{"x":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(root, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	opened, appErr := manager.Open(context.Background(), OpenOptions{Path: "data.json", Format: core.FormatJSON})
	if appErr != nil {
		t.Fatal(appErr)
	}
	lease, appErr := manager.Acquire(context.Background(), opened.FileID)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer lease.Release()
	if _, err := io.ReadAll(lease.File()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- os.WriteFile(path, []byte(`{"x":9999}`), 0600) }()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := lease.Validate(); err == nil || err.Code != core.CodeSourceChanged {
		t.Fatalf("post-scan validation=%v", err)
	}
	manager.mu.Lock()
	_, exists := manager.handles[opened.FileID]
	manager.mu.Unlock()
	if exists {
		t.Fatal("changed source handle was retained")
	}
}

func TestBusyLeaseDoesNotBlockAcquisitionOrExpiryCleanup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	limits := core.DefaultLimits()
	limits.HandleTTL = time.Second
	manager, err := NewManager(root, limits, ManagerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	opened, appErr := manager.Open(context.Background(), OpenOptions{Path: "data.json"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	lease, appErr := manager.Acquire(context.Background(), opened.FileID)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer lease.Release()
	if _, err := manager.Acquire(context.Background(), opened.FileID); err == nil || err.Code != core.CodeResourceLimit {
		t.Fatalf("busy error=%v", err)
	}
	now = now.Add(2 * time.Second)
	// Opening another source runs expiry cleanup while the old lease remains held.
	second, appErr := manager.Open(context.Background(), OpenOptions{Path: "data.json"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, err := manager.CloseHandle(second.FileID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Acquire(ctx, opened.FileID); err == nil || err.Code != core.CodeCancelled {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestConcurrentOpenLimitDetailsAreRaceFree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	limits.MaxOpenFiles = 1
	manager, err := NewManager(root, limits, ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Go(func() {
			for attempt := 0; attempt < 30; attempt++ {
				opened, appErr := manager.Open(context.Background(), OpenOptions{Path: "data.json"})
				if appErr != nil {
					if appErr.Code != core.CodeResourceLimit {
						t.Errorf("Open: %v", appErr)
					}
					continue
				}
				if _, err := manager.CloseHandle(opened.FileID); err != nil {
					t.Errorf("Close: %v", err)
				}
			}
		})
	}
	workers.Wait()
}
