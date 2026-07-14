package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oovz/mcp-json-reader/v2/internal/core"
	"github.com/oovz/mcp-json-reader/v2/internal/service"
	"github.com/oovz/mcp-json-reader/v2/internal/source"
)

func TestServerListsV1ToolsAndReturnsStructuredToolErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"id":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	manager, err := source.NewManager(root, limits, source.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	server := New(service.New(manager, limits, service.Options{}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, connectErr := client.Connect(ctx, clientTransport, nil)
	if connectErr != nil {
		t.Fatalf("Connect error: %v", connectErr)
	}
	t.Cleanup(func() { _ = session.Close() })

	listed, listErr := session.ListTools(ctx, nil)
	if listErr != nil {
		t.Fatalf("ListTools error: %v", listErr)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if !strings.Contains(tool.Description, "standard JSON") && tool.Name != "json_close" {
			t.Errorf("tool %s description does not explain standard JSON: %q", tool.Name, tool.Description)
		}
		outputSchema, ok := tool.OutputSchema.(map[string]any)
		if !ok {
			t.Errorf("tool %s output schema = %#v, want an object schema", tool.Name, tool.OutputSchema)
			continue
		}
		alternatives, ok := outputSchema["oneOf"].([]any)
		if !ok || len(alternatives) != 2 {
			t.Errorf("tool %s output schema = %#v, want exact success/error alternatives", tool.Name, outputSchema)
			continue
		}
		successSchema, successOK := alternatives[0].(map[string]any)
		errorSchema, errorOK := alternatives[1].(map[string]any)
		if !successOK || successSchema["additionalProperties"] != false || successSchema["properties"] == nil {
			t.Errorf("tool %s success schema = %#v, want closed exact object", tool.Name, alternatives[0])
		}
		if !errorOK || errorSchema["additionalProperties"] != false || errorSchema["properties"] == nil {
			t.Errorf("tool %s error schema = %#v, want closed machine-readable error", tool.Name, alternatives[1])
		}
	}
	sort.Strings(names)
	if got, want := strings.Join(names, ","), "json_close,json_open,json_read"; got != want {
		t.Fatalf("tools = %s, want %s", got, want)
	}

	failed, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "json_open", Arguments: map[string]any{"path": "../outside.json", "format": "json"}})
	if callErr != nil {
		t.Fatalf("CallTool protocol error: %v", callErr)
	}
	if !failed.IsError {
		t.Fatalf("CallTool result = %#v, want tool error", failed)
	}
	structured, ok := failed.StructuredContent.(map[string]any)
	if !ok || structured["code"] != string(core.CodeAccessDenied) {
		t.Fatalf("structured error = %#v, want ACCESS_DENIED", failed.StructuredContent)
	}
	if len(failed.Content) != 1 {
		t.Fatalf("error content = %#v, want one JSON text mirror", failed.Content)
	}

	opened, openCallErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "json_open", Arguments: map[string]any{"path": "data.json", "format": "json"}})
	if openCallErr != nil || opened.IsError {
		t.Fatalf("valid json_open = %#v, %v", opened, openCallErr)
	}
	openStructured, ok := opened.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("json_open structured content = %#v", opened.StructuredContent)
	}
	fileID, ok := openStructured["file_id"].(string)
	if !ok || fileID == "" {
		t.Fatalf("json_open file_id = %#v", openStructured["file_id"])
	}

	invalidReads := []map[string]any{
		{"file_id": fileID, "format": "json", "language": "pointer", "query": "/id"},
		{"file_id": fileID, "language": "pointer"},
		{"file_id": fileID, "language": "pointer", "query": nil},
		{"file_id": fileID, "language": "pointer", "query": "/id", "max_items": 0},
	}
	for _, arguments := range invalidReads {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "json_read", Arguments: arguments})
		if err != nil {
			t.Fatalf("json_read(%#v) protocol error: %v", arguments, err)
		}
		structured, structuredOK := result.StructuredContent.(map[string]any)
		if !result.IsError || !structuredOK || structured["code"] != string(core.CodeInvalidArgument) {
			t.Errorf("json_read(%#v) = %#v, want INVALID_ARGUMENT tool error", arguments, result)
		}
	}

	rootPointer, rootErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "json_read", Arguments: map[string]any{"file_id": fileID, "language": "pointer", "query": ""}})
	if rootErr != nil || rootPointer.IsError {
		t.Fatalf("explicit empty root Pointer = %#v, %v, want success", rootPointer, rootErr)
	}
	closedResult, closedErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "json_close", Arguments: map[string]any{"file_id": fileID}})
	if closedErr != nil || closedResult.IsError {
		t.Fatalf("json_close = %#v, %v, want success", closedResult, closedErr)
	}
}

func TestCurrentVersionHonorsReleaseOverride(t *testing.T) {
	original := Version
	Version = "v9.8.7-test"
	t.Cleanup(func() { Version = original })
	if got := CurrentVersion(); got != "9.8.7-test" {
		t.Fatalf("CurrentVersion = %q, want linker override", got)
	}
}

func TestArgumentDecodersRejectEveryInvalidRequestShape(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		decode func(json.RawMessage) *core.AppError
	}{
		{name: "open non-object", raw: `[]`, decode: openDecodeError},
		{name: "open missing path", raw: `{}`, decode: openDecodeError},
		{name: "open empty path", raw: `{"path":""}`, decode: openDecodeError},
		{name: "open empty format", raw: `{"path":"data","format":""}`, decode: openDecodeError},
		{name: "open empty validation", raw: `{"path":"data","validation":""}`, decode: openDecodeError},
		{name: "open unknown field", raw: `{"path":"data","extra":true}`, decode: openDecodeError},
		{name: "open wrong path type", raw: `{"path":1}`, decode: openDecodeError},
		{name: "open trailing data", raw: `{"path":"data"} true`, decode: openDecodeError},
		{name: "read non-object", raw: `[]`, decode: readDecodeError},
		{name: "read missing source", raw: `{"language":"pointer","query":""}`, decode: readDecodeError},
		{name: "read both sources", raw: `{"file_id":"id","path":"data","language":"pointer","query":""}`, decode: readDecodeError},
		{name: "read empty file id", raw: `{"file_id":"","language":"pointer","query":""}`, decode: readDecodeError},
		{name: "read empty path", raw: `{"path":"","language":"pointer","query":""}`, decode: readDecodeError},
		{name: "read missing language", raw: `{"path":"data","query":""}`, decode: readDecodeError},
		{name: "read missing query", raw: `{"path":"data","language":"pointer"}`, decode: readDecodeError},
		{name: "read null query", raw: `{"path":"data","language":"pointer","query":null}`, decode: readDecodeError},
		{name: "read format with file id", raw: `{"file_id":"id","format":"json","language":"pointer","query":""}`, decode: readDecodeError},
		{name: "read empty path format", raw: `{"path":"data","format":"","language":"pointer","query":""}`, decode: readDecodeError},
		{name: "read empty cursor", raw: `{"cursor":""}`, decode: readDecodeError},
		{name: "read mixed cursor", raw: `{"cursor":"cursor","query":""}`, decode: readDecodeError},
		{name: "read zero items", raw: `{"path":"data","language":"pointer","query":"","max_items":0}`, decode: readDecodeError},
		{name: "read negative result bytes", raw: `{"path":"data","language":"pointer","query":"","max_result_bytes":-1}`, decode: readDecodeError},
		{name: "read fractional items", raw: `{"path":"data","language":"pointer","query":"","max_items":1.5}`, decode: readDecodeError},
		{name: "read wrong cursor type", raw: `{"cursor":1}`, decode: readDecodeError},
		{name: "read unknown field", raw: `{"cursor":"cursor","extra":true}`, decode: readDecodeError},
		{name: "read trailing data", raw: `{"cursor":"cursor"} null`, decode: readDecodeError},
		{name: "close non-object", raw: `[]`, decode: closeDecodeError},
		{name: "close missing file id", raw: `{}`, decode: closeDecodeError},
		{name: "close empty file id", raw: `{"file_id":""}`, decode: closeDecodeError},
		{name: "close wrong file id type", raw: `{"file_id":1}`, decode: closeDecodeError},
		{name: "close unknown field", raw: `{"file_id":"id","extra":true}`, decode: closeDecodeError},
		{name: "close trailing data", raw: `{"file_id":"id"} false`, decode: closeDecodeError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.decode(json.RawMessage(test.raw))
			if err == nil || err.Code != core.CodeInvalidArgument {
				t.Fatalf("decode(%s) error = %#v, want INVALID_ARGUMENT", test.raw, err)
			}
		})
	}

	validRootPointer := json.RawMessage(`{"path":"data","language":"pointer","query":""}`)
	if _, err := decodeReadArguments(validRootPointer); err != nil {
		t.Fatalf("explicit empty root Pointer rejected: %v", err)
	}
}

func TestErrorOutputSchemaListsExactlyTheStableApplicationCodes(t *testing.T) {
	properties := errorOutputSchema()["properties"].(map[string]any)
	codeSchema := properties["code"].(map[string]any)
	got := append([]string(nil), codeSchema["enum"].([]string)...)
	sort.Strings(got)
	want := []string{
		string(core.CodeAccessDenied), string(core.CodeCancelled), string(core.CodeFormatMismatch),
		string(core.CodeHandleExpired), string(core.CodeIO), string(core.CodeInternal),
		string(core.CodeInvalidArgument), string(core.CodeQuerySyntax), string(core.CodeResourceLimit),
		string(core.CodeSourceChanged), string(core.CodeSyntax), string(core.CodeUnsupportedFormat),
		string(core.CodeUnsupportedQueryFeature), string(core.CodeUnsupportedSyntax),
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("error code enum = %v, want %v", got, want)
	}
}

func openDecodeError(raw json.RawMessage) *core.AppError {
	_, err := decodeOpenArguments(raw)
	return err
}

func readDecodeError(raw json.RawMessage) *core.AppError {
	_, err := decodeReadArguments(raw)
	return err
}

func closeDecodeError(raw json.RawMessage) *core.AppError {
	_, err := decodeCloseArguments(raw)
	return err
}
