package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime/debug"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oovz/mcp-json-reader/v2/internal/core"
	"github.com/oovz/mcp-json-reader/v2/internal/service"
)

var Version = ""

func CurrentVersion() string {
	if Version != "" {
		return strings.TrimPrefix(Version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "(devel)"
}

func New(jsonService *service.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-json-reader",
		Title:   "MCP JSON Reader",
		Version: CurrentVersion(),
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:         "json_open",
		Title:        "Open JSON file",
		Description:  "Open and inspect a local standard JSON file without loading the entire file into memory. Supports one JSON document, JSON Lines/NDJSON, and record-separator-delimited JSON sequences. Returns a process-scoped file handle for repeated reads.",
		InputSchema:  openInputSchema(),
		OutputSchema: openOutputSchema(),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		input, decodeErr := decodeOpenArguments(request.Params.Arguments)
		if decodeErr != nil {
			return errorResult(decodeErr), nil
		}
		output, appErr := jsonService.Open(ctx, input)
		if appErr != nil {
			return errorResult(appErr), nil
		}
		return successResult(output), nil
	})

	server.AddTool(&mcp.Tool{
		Name:         "json_read",
		Title:        "Read JSON values",
		Description:  "Read matching standard JSON values from an opened local file or directly from a local path. Use pointer for an exact location such as /orders/0/id, or the bounded forward-streaming JSONPath profile for selections such as $.orders[*].id and $.orders[?@.total > 100].id. Results are bounded and paginated.",
		InputSchema:  readInputSchema(),
		OutputSchema: readOutputSchema(),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		input, decodeErr := decodeReadArguments(request.Params.Arguments)
		if decodeErr != nil {
			return errorResult(decodeErr), nil
		}
		output, appErr := jsonService.Read(ctx, input)
		if appErr != nil {
			return errorResult(appErr), nil
		}
		return successResult(output), nil
	})

	server.AddTool(&mcp.Tool{
		Name:         "json_close",
		Title:        "Close JSON file",
		Description:  "Release a process-scoped JSON file handle and its pagination cursors. This operation is idempotent.",
		InputSchema:  closeInputSchema(),
		OutputSchema: closeOutputSchema(),
		Annotations:  &mcp.ToolAnnotations{DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		input, decodeErr := decodeCloseArguments(request.Params.Arguments)
		if decodeErr != nil {
			return errorResult(decodeErr), nil
		}
		output, appErr := jsonService.Close(ctx, input)
		if appErr != nil {
			return errorResult(appErr), nil
		}
		return successResult(output), nil
	})
	return server
}

func decodeArguments[T any](raw json.RawMessage) (T, map[string]json.RawMessage, *core.AppError) {
	var output T
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return output, nil, invalidArguments("tool arguments do not match the declared schema")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return output, nil, invalidArguments("tool arguments contain trailing data")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return output, nil, invalidArguments("tool arguments must be a JSON object")
	}
	return output, fields, nil
}

func decodeOpenArguments(raw json.RawMessage) (service.OpenInput, *core.AppError) {
	input, fields, err := decodeArguments[service.OpenInput](raw)
	if err != nil {
		return input, err
	}
	if _, present := fields["path"]; !present || input.Path == "" {
		return input, invalidArguments("path is required")
	}
	if _, present := fields["format"]; present && input.Format == "" {
		return input, invalidArguments("format cannot be empty")
	}
	if _, present := fields["validation"]; present && input.Validation == "" {
		return input, invalidArguments("validation cannot be empty")
	}
	return input, nil
}

func decodeReadArguments(raw json.RawMessage) (service.ReadInput, *core.AppError) {
	input, fields, err := decodeArguments[service.ReadInput](raw)
	if err != nil {
		return input, err
	}
	_, hasCursor := fields["cursor"]
	if hasCursor {
		if input.Cursor == "" {
			return input, invalidArguments("cursor cannot be empty")
		}
		if hasAnyField(fields, "file_id", "path", "format", "language", "query", "max_items", "max_result_bytes") {
			return input, invalidArguments("cursor continuation must be supplied by itself")
		}
		return input, nil
	}

	_, hasFileID := fields["file_id"]
	_, hasPath := fields["path"]
	if hasFileID == hasPath {
		return input, invalidArguments("provide exactly one of file_id or path")
	}
	if hasFileID && input.FileID == "" {
		return input, invalidArguments("file_id cannot be empty")
	}
	if hasPath && input.Path == "" {
		return input, invalidArguments("path cannot be empty")
	}
	if _, present := fields["language"]; !present || input.Language == "" {
		return input, invalidArguments("language is required")
	}
	queryValue, present := fields["query"]
	if !present {
		return input, invalidArguments("query is required; use an explicit empty string for the root Pointer")
	}
	if bytes.Equal(bytes.TrimSpace(queryValue), []byte("null")) {
		return input, invalidArguments("query must be a string; use an explicit empty string for the root Pointer")
	}
	if hasFileID {
		if _, present := fields["format"]; present {
			return input, invalidArguments("format is only valid with an implicit path source")
		}
	} else if _, present := fields["format"]; present && input.Format == "" {
		return input, invalidArguments("format cannot be empty")
	}
	if _, present := fields["max_items"]; present && input.MaxItems <= 0 {
		return input, invalidArguments("max_items must be greater than zero")
	}
	if _, present := fields["max_result_bytes"]; present && input.MaxResultBytes <= 0 {
		return input, invalidArguments("max_result_bytes must be greater than zero")
	}
	return input, nil
}

func decodeCloseArguments(raw json.RawMessage) (service.CloseInput, *core.AppError) {
	input, fields, err := decodeArguments[service.CloseInput](raw)
	if err != nil {
		return input, err
	}
	if _, present := fields["file_id"]; !present || input.FileID == "" {
		return input, invalidArguments("file_id is required")
	}
	return input, nil
}

func hasAnyField(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		if _, present := fields[name]; present {
			return true
		}
	}
	return false
}

func invalidArguments(message string) *core.AppError {
	return &core.AppError{Code: core.CodeInvalidArgument, Message: message}
}

func successResult(output any) *mcp.CallToolResult {
	encoded, err := json.Marshal(output)
	if err != nil {
		return errorResult(&core.AppError{Code: core.CodeInternal, Message: "cannot encode tool result"})
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: output,
	}
}

func errorResult(appErr *core.AppError) *mcp.CallToolResult {
	encoded, err := json.Marshal(appErr)
	if err != nil {
		encoded = []byte(`{"code":"INTERNAL_ERROR","message":"cannot encode tool error"}`)
		appErr = &core.AppError{Code: core.CodeInternal, Message: "cannot encode tool error"}
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: appErr,
		IsError:           true,
	}
}

func openInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"path"},
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "minLength": 1, "description": "Path to a local file within the server's configured root."},
			"format": formatSchema(),
			"validation": map[string]any{
				"type": "string", "enum": []string{"probe", "full"}, "default": "probe",
				"description": "probe examines a bounded prefix and may be incomplete. full streams through and validates the complete file.",
			},
		},
	}
}

func readInputSchema() map[string]any {
	properties := map[string]any{
		"file_id": map[string]any{"type": "string", "minLength": 1, "description": "Process-scoped handle returned by json_open. A file_id exposed only for implicit pagination may be closed but must be continued with its cursor."},
		"path":    map[string]any{"type": "string", "minLength": 1, "description": "Local path to open implicitly for this read."},
		"format":  formatSchema(),
		"language": map[string]any{
			"type": "string", "enum": []string{"pointer", "jsonpath"},
			"description": "pointer is an exact RFC 6901 path such as /orders/0/id. jsonpath is a bounded forward-streaming RFC 9535 profile such as $.orders[*].id or $.orders[?@.total > 100].id.",
		},
		"query":            map[string]any{"type": "string", "description": "Pointer or JSONPath expression. The empty string is the Pointer for the root value."},
		"cursor":           map[string]any{"type": "string", "minLength": 1, "description": "Opaque process-scoped continuation returned by a previous json_read. Supply it by itself."},
		"max_items":        map[string]any{"type": "integer", "minimum": 1, "description": "Optional per-page item cap, bounded by the server maximum."},
		"max_result_bytes": map[string]any{"type": "integer", "minimum": 1, "description": "Optional hard byte cap for the serialized read result, bounded by the server maximum."},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"oneOf": []any{
			map[string]any{"required": []string{"cursor"}, "not": map[string]any{"anyOf": requiredAny("file_id", "path", "format", "language", "query", "max_items", "max_result_bytes")}},
			map[string]any{"required": []string{"file_id", "language", "query"}, "not": map[string]any{"anyOf": requiredAny("path", "cursor", "format")}},
			map[string]any{"required": []string{"path", "language", "query"}, "not": map[string]any{"anyOf": requiredAny("file_id", "cursor")}},
		},
	}
}

func closeInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"file_id"},
		"properties": map[string]any{
			"file_id": map[string]any{"type": "string", "minLength": 1, "description": "Process-scoped handle returned by json_open or an implicitly paginated json_read."},
		},
	}
}

func formatSchema() map[string]any {
	return map[string]any{
		"type": "string", "enum": []string{"auto", "json", "jsonl", "json-seq"}, "default": "auto",
		"description": "auto detects json, jsonl, or json-seq from the extension and a bounded sample, never JSONC/JSON5. json is one standard JSON value. jsonl requires exactly one standard JSON value on every physical line (blank lines are invalid) and is queried as a virtual array. json-seq uses ASCII record separator 0x1E and is queried as a virtual array.",
	}
}

func openOutputSchema() map[string]any {
	return toolOutputSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"file_id", "requested_format", "detected_format", "validation", "source"},
		"properties": map[string]any{
			"file_id":          map[string]any{"type": "string", "minLength": 1},
			"requested_format": formatValueSchema(true),
			"detected_format":  formatValueSchema(false),
			"detection": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"basis", "confidence"},
				"properties": map[string]any{"basis": map[string]any{"type": "string"}, "confidence": map[string]any{"type": "string", "enum": []string{"medium", "high"}}},
			},
			"validation": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"mode", "complete", "bytes_examined"},
				"properties": map[string]any{
					"mode": map[string]any{"type": "string", "enum": []string{"probe", "full"}}, "complete": map[string]any{"type": "boolean"},
					"bytes_examined": map[string]any{"type": "integer", "minimum": 0}, "records_examined": map[string]any{"type": "integer", "minimum": 0},
				},
			},
			"source": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"size", "modified_time"},
				"properties": map[string]any{"size": map[string]any{"type": "integer", "minimum": 0}, "modified_time": map[string]any{"type": "string"}},
			},
		},
	})
}

func readOutputSchema() map[string]any {
	return toolOutputSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"language", "query", "items", "complete", "stats"},
		"properties": map[string]any{
			"file_id":     map[string]any{"type": "string", "minLength": 1},
			"language":    map[string]any{"type": "string", "enum": []string{"pointer", "jsonpath"}},
			"query":       map[string]any{"type": "string"},
			"next_cursor": map[string]any{"type": "string", "minLength": 1},
			"complete":    map[string]any{"type": "boolean"},
			"items": map[string]any{
				"type": "array", "items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"path", "value"},
					"properties": map[string]any{"path": map[string]any{"type": "string"}, "value": map[string]any{}},
				},
			},
			"stats": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"returned", "skipped"},
				"properties": map[string]any{"returned": map[string]any{"type": "integer", "minimum": 0}, "skipped": map[string]any{"type": "integer", "minimum": 0}},
			},
		},
	})
}

func closeOutputSchema() map[string]any {
	return toolOutputSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"file_id", "closed"},
		"properties": map[string]any{
			"file_id": map[string]any{"type": "string", "minLength": 1},
			"closed":  map[string]any{"type": "boolean"},
		},
	})
}

func toolOutputSchema(success map[string]any) map[string]any {
	return map[string]any{"type": "object", "oneOf": []any{success, errorOutputSchema()}}
}

func errorOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"code", "message"},
		"properties": map[string]any{
			"code": map[string]any{"type": "string", "enum": []string{
				"UNSUPPORTED_FORMAT", "FORMAT_MISMATCH", "SYNTAX_ERROR", "UNSUPPORTED_SYNTAX",
				"QUERY_SYNTAX_ERROR", "UNSUPPORTED_QUERY_FEATURE", "RESOURCE_LIMIT_EXCEEDED", "SOURCE_CHANGED",
				"HANDLE_EXPIRED", "INVALID_ARGUMENT", "ACCESS_DENIED", "IO_ERROR", "CANCELLED", "INTERNAL_ERROR",
			}},
			"message":         map[string]any{"type": "string"},
			"expected_format": formatValueSchema(false),
			"likely_formats":  map[string]any{"type": "array", "items": formatValueSchema(false)},
			"hint":            map[string]any{"type": "string"},
			"location": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"byte_offset": map[string]any{"type": "integer", "minimum": 0}, "line": map[string]any{"type": "integer", "minimum": 1},
					"column": map[string]any{"type": "integer", "minimum": 1}, "record_index": map[string]any{"type": "integer", "minimum": 0}, "path": map[string]any{"type": "string"},
				},
			},
			"retry": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"format"}, "properties": map[string]any{"format": formatValueSchema(false)},
			},
			"limit": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"name", "limit"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}, "value": map[string]any{"type": "integer"}},
			},
		},
	}
}

func formatValueSchema(includeAuto bool) map[string]any {
	values := []string{"json", "jsonl", "json-seq"}
	if includeAuto {
		values = append([]string{"auto"}, values...)
	}
	return map[string]any{"type": "string", "enum": values}
}

func requiredAny(names ...string) []any {
	items := make([]any, 0, len(names))
	for _, name := range names {
		items = append(items, map[string]any{"required": []string{name}})
	}
	return items
}

func boolPointer(value bool) *bool { return &value }
