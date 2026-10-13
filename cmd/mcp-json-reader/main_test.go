package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBinaryServesToolsOverStdio(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"orders":[{"id":1},{"id":2}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := buildServerBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	command := exec.Command(executable, "--root", root)
	command.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-e2e-test", Version: "1.0.0"}, nil)
	session, connectErr := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if connectErr != nil {
		t.Fatalf("connecting to subprocess: %v; stderr: %s", connectErr, stderr.String())
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = session.Close()
		}
	})

	listed, listErr := session.ListTools(ctx, nil)
	if listErr != nil {
		t.Fatalf("listing tools: %v", listErr)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "json_close,json_open,json_read" {
		t.Fatalf("tools = %v", names)
	}

	opened, openErr := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "json_open",
		Arguments: map[string]any{"path": "data.json", "format": "json", "validation": "full"},
	})
	if openErr != nil || opened.IsError {
		t.Fatalf("json_open = %#v, %v", opened, openErr)
	}
	openContent, ok := opened.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("json_open structured content = %#v", opened.StructuredContent)
	}
	fileID, ok := openContent["file_id"].(string)
	if !ok || fileID == "" {
		t.Fatalf("json_open file_id = %#v", openContent["file_id"])
	}

	read, readErr := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "json_read",
		Arguments: map[string]any{"file_id": fileID, "language": "jsonpath", "query": "$.orders[*].id"},
	})
	if readErr != nil || read.IsError {
		t.Fatalf("json_read = %#v, %v", read, readErr)
	}
	readContent, ok := read.StructuredContent.(map[string]any)
	if !ok || readContent["complete"] != true {
		t.Fatalf("json_read structured content = %#v", read.StructuredContent)
	}
	items, ok := readContent["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("json_read items = %#v", readContent["items"])
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["path"] != "/orders/0/id" || first["value"] != float64(1) {
		t.Fatalf("first json_read item = %#v", items[0])
	}

	closedResult, closeErr := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "json_close",
		Arguments: map[string]any{"file_id": fileID},
	})
	if closeErr != nil || closedResult.IsError {
		t.Fatalf("json_close = %#v, %v", closedResult, closeErr)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing subprocess session: %v; stderr: %s", err, stderr.String())
	}
	closed = true
	if stderr.Len() != 0 {
		t.Fatalf("subprocess wrote non-protocol diagnostics on a successful run: %q", stderr.String())
	}
}

func buildServerBinary(t *testing.T) string {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "mcp-json-reader")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", executable, "./cmd/mcp-json-reader")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building binary: %v\n%s", err, output)
	}
	return executable
}

func TestBinaryGlobalAndConfiguredWorkspaceAccess(t *testing.T) {
	t.Setenv("MCP_JSON_ROOT", "")
	executable := buildServerBinary(t)
	first, second, outside := t.TempDir(), t.TempDir(), t.TempDir()
	for _, directory := range []string{first, second, outside} {
		if err := os.WriteFile(filepath.Join(directory, "data.json"), []byte(`{"id":1}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(t.TempDir(), "workspaces.json")
	config, err := json.Marshal(map[string]any{"workspaces": []string{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	for _, restricted := range []bool{false, true} {
		name := "global"
		var args []string
		if restricted {
			name, args = "workspaces", []string{"--config", configPath}
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.Command(executable, args...)
			command.Dir = t.TempDir()
			var stderr bytes.Buffer
			command.Stderr = &stderr
			client := mcp.NewClient(&mcp.Implementation{Name: "workspace-test", Version: "1.0.0"}, nil)
			session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
			if err != nil {
				t.Fatalf("connect: %v; stderr: %s", err, stderr.String())
			}
			defer func() { _ = session.Close() }()
			for _, directory := range []string{first, second, outside} {
				result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "json_read", Arguments: map[string]any{
					"path": filepath.Join(directory, "data.json"), "language": "pointer", "query": "/id",
				}})
				if err != nil {
					t.Fatal(err)
				}
				if restricted && directory == outside {
					content, ok := result.StructuredContent.(map[string]any)
					if !result.IsError || !ok || content["code"] != "ACCESS_DENIED" {
						t.Fatalf("outside read = %#v", result)
					}
				} else if result.IsError {
					t.Fatalf("allowed read = %#v", result)
				}
			}
		})
	}
}
