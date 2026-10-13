package source

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

func TestGlobalAccessUsesProcessDirectoryAndDetectsReplacement(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	path := filepath.Join(root, "data.json")
	if err := os.WriteFile(path, []byte(`{"id":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(nil, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	for _, requested := range []string{"data.json", path} {
		opened, appErr := manager.Open(context.Background(), OpenOptions{Path: requested})
		if appErr != nil {
			t.Fatal(appErr)
		}
		if err := os.WriteFile(path, []byte(`{"id":12345}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, appErr := manager.Acquire(context.Background(), opened.FileID); appErr == nil || appErr.Code != core.CodeSourceChanged {
			t.Fatalf("changed source result = %v", appErr)
		}
		if err := os.WriteFile(path, []byte(`{"id":1}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMultipleWorkspaceAccess(t *testing.T) {
	first, second, outside := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{first, second, outside} {
		if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`null`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewManager([]string{first, second}, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	for _, root := range []string{first, second} {
		opened, appErr := manager.Open(context.Background(), OpenOptions{Path: filepath.Join(root, "data.json")})
		if appErr != nil {
			t.Fatal(appErr)
		}
		lease, appErr := manager.Acquire(context.Background(), opened.FileID)
		if appErr != nil {
			t.Fatal(appErr)
		}
		lease.Release()
	}
	for _, test := range []struct {
		path string
		code core.ErrorCode
	}{
		{filepath.Join(outside, "data.json"), core.CodeAccessDenied},
		{"data.json", core.CodeInvalidArgument},
		{filepath.Join(first, "..", filepath.Base(outside), "data.json"), core.CodeAccessDenied},
	} {
		if _, appErr := manager.Open(context.Background(), OpenOptions{Path: test.path}); appErr == nil || appErr.Code != test.code {
			t.Fatalf("Open(%q) = %v, want %s", test.path, appErr, test.code)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "data.json"), filepath.Join(first, "escape.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, appErr := manager.Open(context.Background(), OpenOptions{Path: filepath.Join(first, "escape.json")}); appErr == nil || appErr.Code != core.CodeAccessDenied {
		t.Fatalf("symlink escape = %v", appErr)
	}
}

func TestGlobalAccessFollowsSymlinksAndRejectsDirectories(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(outside, "data.json")
	if err := os.WriteFile(path, []byte(`null`), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(nil, core.DefaultLimits(), ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	if _, appErr := manager.Open(context.Background(), OpenOptions{Path: root}); appErr == nil || appErr.Code != core.CodeInvalidArgument {
		t.Fatalf("directory = %v", appErr)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	opened, appErr := manager.Open(context.Background(), OpenOptions{Path: link})
	if appErr != nil {
		t.Fatal(appErr)
	}
	lease, appErr := manager.Acquire(context.Background(), opened.FileID)
	if appErr != nil {
		t.Fatal(appErr)
	}
	lease.Release()
}

func TestNestedWorkspaceSymlinkIsIndependentOfListOrder(t *testing.T) {
	parent, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "data.json"), []byte(`null`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, paths := range [][]string{{parent, link}, {link, parent}} {
		manager, err := NewManager(paths, core.DefaultLimits(), ManagerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, appErr := manager.Open(context.Background(), OpenOptions{Path: filepath.Join(link, "data.json")})
		_ = manager.Shutdown()
		if appErr != nil {
			t.Fatalf("workspace order %v: %v", paths, appErr)
		}
	}
}

func TestNestedWorkspacePreservesContainingWorkspaceAccess(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	child := filepath.Join(parent, "sub")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	const data = `{"id":1}`
	for _, directory := range []string{parent, outside} {
		if err := os.WriteFile(filepath.Join(directory, "data.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(child, "link.json")
	if err := os.Symlink(filepath.Join("..", "data.json"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	escape := filepath.Join(child, "escape.json")
	if err := os.Symlink(filepath.Join(outside, "data.json"), escape); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{parent}, {parent, child}, {child, parent}} {
		manager, err := NewManager(paths, core.DefaultLimits(), ManagerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = manager.Shutdown() })
		opened, appErr := manager.Open(context.Background(), OpenOptions{Path: link})
		if appErr != nil {
			t.Fatalf("workspaces %v: %v", paths, appErr)
		}
		lease, appErr := manager.Acquire(context.Background(), opened.FileID)
		if appErr != nil {
			t.Fatal(appErr)
		}
		contents, readErr := io.ReadAll(lease.File())
		lease.Release()
		if readErr != nil || string(contents) != data {
			t.Fatalf("workspaces %v read %q, %v", paths, contents, readErr)
		}
		if _, appErr := manager.Open(context.Background(), OpenOptions{Path: escape}); appErr == nil || appErr.Code != core.CodeAccessDenied {
			t.Fatalf("workspaces %v allowed an escape: %v", paths, appErr)
		}
	}
}
