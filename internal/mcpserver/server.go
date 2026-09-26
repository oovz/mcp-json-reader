package mcpserver

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"runtime/debug"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/service"
)

var Version = ""

const currentProtocolVersion = "2026-07-28"

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
	}, &mcp.ServerOptions{
		Capabilities:              &mcp.ServerCapabilities{},
		SupportedProtocolVersions: []string{currentProtocolVersion},
	})
	server.AddReceivingMiddleware(rejectLegacyMethods)

	server.AddTool(&mcp.Tool{
		Name:         "json_open",
		Title:        "Open JSON file",
		Description:  "Open and inspect a local standard JSON file without loading the entire file into memory. Supports one JSON document, JSON Lines/NDJSON, and record-separator-delimited JSON sequences. Returns a process-scoped file handle for repeated reads.",
		InputSchema:  openArgumentsSchema.Schema(),
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
		InputSchema:  readArgumentsSchema.Schema(),
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
		InputSchema:  closeArgumentsSchema.Schema(),
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

// rejectLegacyMethods prevents the SDK's legacy lifecycle from establishing a
// session. StdioTransport checks request metadata before SDK dispatch.
func rejectLegacyMethods(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		switch method {
		case "initialize", "ping", "notifications/initialized":
			return nil, unsupportedProtocolError("")
		}
		return next(ctx, method, request)
	}
}

func unsupportedProtocolError(requested string) error {
	data, _ := json.Marshal(mcp.UnsupportedProtocolVersionData{
		Supported: []string{currentProtocolVersion},
		Requested: requested,
	})
	return &jsonrpc.Error{
		Code:    mcp.CodeUnsupportedProtocolVersion,
		Message: "only MCP 2026-07-28 is supported",
		Data:    data,
	}
}

// Resolve the three closed schemas once. Invalid built-in schemas are a
// programming error; request validation always uses these exact schemas.
var (
	openArgumentsSchema  = mustResolveSchema(openInputSchema())
	readArgumentsSchema  = mustResolveSchema(readInputSchema())
	closeArgumentsSchema = mustResolveSchema(closeInputSchema())
)

func mustResolveSchema(value map[string]any) *jsonschema.Resolved {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	return resolved
}

func decodeArguments[T any](raw json.RawMessage, schema *jsonschema.Resolved) (T, *core.AppError) {
	var output T
	// Validate the original JSON before map decoding can collapse duplicate keys
	// or a permissive decoder can replace malformed Unicode.
	if !jsontext.Value(raw).IsValid() {
		return output, invalidArguments("tool arguments must contain one strict JSON object")
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return output, invalidArguments("tool arguments contain invalid values")
	}
	if err := schema.Validate(instance); err != nil {
		return output, invalidArguments("tool arguments do not match the declared schema")
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		return output, invalidArguments("tool arguments exceed supported value ranges")
	}
	return output, nil
}

func decodeOpenArguments(raw json.RawMessage) (service.OpenInput, *core.AppError) {
	return decodeArguments[service.OpenInput](raw, openArgumentsSchema)
}
func decodeReadArguments(raw json.RawMessage) (service.ReadInput, *core.AppError) {
	return decodeArguments[service.ReadInput](raw, readArgumentsSchema)
}
func decodeCloseArguments(raw json.RawMessage) (service.CloseInput, *core.AppError) {
	return decodeArguments[service.CloseInput](raw, closeArgumentsSchema)
}

func invalidArguments(message string) *core.AppError {
	return &core.AppError{Code: core.CodeInvalidArgument, Message: message}
}

func successResult(output any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{}, StructuredContent: output}
}

func errorResult(appErr *core.AppError) *mcp.CallToolResult {
	bounded := *appErr
	bounded.Message = diagnosticText(bounded.Message)
	bounded.Hint = diagnosticText(bounded.Hint)
	if bounded.Location != nil {
		location := *bounded.Location
		location.Path = diagnosticText(location.Path)
		bounded.Location = &location
	}
	return &mcp.CallToolResult{Content: []mcp.Content{}, StructuredContent: &bounded, IsError: true}
}

func diagnosticText(value string) string {
	const maximum = 1024
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
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
		"max_result_bytes": map[string]any{"type": "integer", "minimum": 1, "description": "Optional hard UTF-8 byte cap for the serialized structuredContent object, including paths, values, query, cursor, and statistics. Protocol envelopes and error diagnostics have separate overhead. Bounded by the server maximum."},
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
		"description": "auto detects json, jsonl, or json-seq from the extension and a bounded sample. json is one standard JSON value. jsonl requires one standard JSON value per physical line and is queried as a virtual array. Blank lines are invalid. json-seq uses ASCII record separator 0x1E and is queried as a virtual array.",
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
