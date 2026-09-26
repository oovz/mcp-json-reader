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
		manager, err := NewManager(root, core.DefaultLimits(), ManagerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Shutdown()
		_, appErr := manager.Open(context.Background(), OpenOptions{Path: "input.fifo", Format: core.FormatJSON})
		if appErr == nil || appErr.Code != core.CodeInvalidArgument {
			t.Fatalf("FIFO result=%v", appErr)
		}
		return
	}
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "input.fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFIFOCannotBlockSourceOpen$")
	command.Env = append(os.Environ(), "MCP_JSON_FIFO_TEST_ROOT="+root)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("FIFO subprocess failed: %v\n%s", err, output)
	}
}
