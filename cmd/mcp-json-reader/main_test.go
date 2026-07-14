package main

import (
	"bytes"
	"context"
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
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "mcp-json-reader")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, "go", "build", "-trimpath", "-o", executable, "./cmd/mcp-json-reader")
	build.Dir = moduleRoot
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("building binary: %v\n%s", buildErr, output)
	}

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

func TestV2ReleaseMetadataUsesTheMajorVersionModulePath(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		want string
	}{
		{path: "go.mod", want: "module github.com/oovz/mcp-json-reader/v2"},
		{path: filepath.Join(".github", "workflows", "release.yml"), want: "-X github.com/oovz/mcp-json-reader/v2/internal/mcpserver.Version="},
		{path: "README.md", want: "go install github.com/oovz/mcp-json-reader/v2/cmd/mcp-json-reader@v2.0.0"},
	}
	for _, test := range tests {
		contents, readErr := os.ReadFile(filepath.Join(moduleRoot, test.path))
		if readErr != nil {
			t.Fatalf("reading %s: %v", test.path, readErr)
		}
		if !strings.Contains(string(contents), test.want) {
			t.Errorf("%s does not contain %q", test.path, test.want)
		}
	}
}
