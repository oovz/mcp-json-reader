//go:build unix

package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

func TestFIFOCannotBlockSourceOpen(t *testing.T) {
	if root := os.Getenv("MCP_JSON_FIFO_TEST_ROOT"); root != "" {
		workspaces, requested := []string{root}, "input.fifo"
		if os.Getenv("MCP_JSON_FIFO_TEST_GLOBAL") == "1" {
			workspaces, requested = nil, filepath.Join(root, requested)
		}
		manager, err := NewManager(workspaces, core.DefaultLimits(), ManagerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Shutdown()
		_, appErr := manager.Open(context.Background(), OpenOptions{Path: requested, Format: core.FormatJSON})
		if appErr == nil || appErr.Code != core.CodeInvalidArgument {
			t.Fatalf("FIFO result=%v", appErr)
		}
		return
	}
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "input.fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, global := range []string{"0", "1"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFIFOCannotBlockSourceOpen$")
		command.Env = append(os.Environ(), "MCP_JSON_FIFO_TEST_ROOT="+root, "MCP_JSON_FIFO_TEST_GLOBAL="+global)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("FIFO subprocess (global=%s) failed: %v\n%s", global, err, output)
		}
	}
}
