# MCP JSON Reader

[![CI](https://github.com/oovz/mcp-json-reader/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/oovz/mcp-json-reader/actions/workflows/ci.yml)
[![Release workflow](https://github.com/oovz/mcp-json-reader/actions/workflows/release.yml/badge.svg)](https://github.com/oovz/mcp-json-reader/actions/workflows/release.yml)
[![npm version](https://img.shields.io/npm/v/mcp-json-reader)](https://www.npmjs.com/package/mcp-json-reader)
[![Go Reference](https://pkg.go.dev/badge/github.com/oovz/mcp-json-reader/v3.svg)](https://pkg.go.dev/github.com/oovz/mcp-json-reader/v3)
[![License](https://img.shields.io/github/license/oovz/mcp-json-reader)](LICENSE)

A local stdio [Model Context Protocol](https://modelcontextprotocol.io/) server for querying JSON, JSON Lines, and JSON-sequence files. It reads files incrementally and bounds input size, nesting, filter candidates, results, and concurrent scans.

Queries use exact [JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901.html) paths or a restricted, forward-streaming [JSONPath](https://www.rfc-editor.org/rfc/rfc9535.html) profile. The server exposes `json_open`, `json_read`, and `json_close`.

## Install

Install with Go **1.27.1 or later**:

```sh
go install github.com/oovz/mcp-json-reader/v3/cmd/mcp-json-reader@latest
mcp-json-reader --root /workspace/data
```

Or run the npm package with Node.js **22.14.0 or later** and npm **11.5.1 or later**:

```sh
npx --yes mcp-json-reader@3 --root /workspace/data
```

For a persistent npm installation:

```sh
npm install --global mcp-json-reader
mcp-json-reader --root /workspace/data
```

Go installs the executable in `GOBIN`, or `$(go env GOPATH)/bin` by default. Add that directory to your `PATH`. The npm package includes native binaries for Linux, macOS, and Windows on amd64 and arm64; its JavaScript launcher selects the matching binary and forwards standard streams, arguments, signals, and exit status.

To build this checkout with Go 1.27.1:

```sh
go build -trimpath -o bin/mcp-json-reader ./cmd/mcp-json-reader
./bin/mcp-json-reader --root /workspace/data
```

On Windows, build an `.exe` and use absolute executable and root paths. Builds can use `CGO_ENABLED=0`.

## Configure an MCP client

Every requested file must be beneath the configured root. Set `MCP_JSON_ROOT` to provide the root when `--root` is omitted. Use a trusted directory containing regular files, and keep source files unchanged while handles are open. See [support](SUPPORT.md) for platforms and [security guidance](SECURITY.md) for file access details.

Example configuration for an installed command:

```json
{
  "mcpServers": {
    "json-reader": {
      "command": "mcp-json-reader",
      "args": ["--root", "/workspace/data"]
    }
  }
}
```

For `npx`:

```json
{
  "mcpServers": {
    "json-reader": {
      "command": "npx",
      "args": ["--yes", "mcp-json-reader@3", "--root", "/workspace/data"]
    }
  }
}
```

The server supports MCP **2026-07-28** over stdio. Clients must support structured tool results, discover tools through `server/discover`, and read result values and errors from `structuredContent`. The `content` array is empty. Required protocol metadata is checked before SDK dispatch; missing or malformed metadata and older protocol versions are rejected. Missing or malformed metadata returns JSON-RPC `-32602`; an unsupported protocol version returns `-32022`.

## Use the tools

### Open a file

`json_open` creates a reusable handle and inspects the source:

```json
{"path":"orders.jsonl","format":"jsonl","validation":"probe"}
```

The response includes a `file_id`, the requested and detected format, validation coverage, and source size and modification time. `probe` checks a bounded prefix; `full` streams the complete source. For JSONL and JSON-seq, probe byte and record limits both apply, including to small files. `validation.complete: false` means some input remains unchecked. An explicit format is authoritative.

### Read with a pointer or JSONPath

Use the `file_id` from `json_open` to read a location with JSON Pointer:

```json
{"file_id":"jf_...","language":"pointer","query":"/0/id"}
```

Or query a file directly with JSONPath:

```json
{"path":"orders.jsonl","format":"jsonl","language":"jsonpath","query":"$[*].id","max_items":2}
```

A result contains an RFC 6901 path and JSON value for each match:

```json
{
  "file_id": "jf_...",
  "language": "jsonpath",
  "query": "$[*].id",
  "items": [
    {"path": "/0/id", "value": 101},
    {"path": "/1/id", "value": 102}
  ],
  "complete": false,
  "next_cursor": "jc_...",
  "stats": {"returned": 2, "skipped": 0}
}
```

Continue a page with its cursor:

```json
{"cursor":"jc_..."}
```

`max_items` sets the page size. `max_result_bytes` caps the UTF-8 JSON encoding of the complete application `structuredContent` object, including values, escaped paths, query, cursor, handle, statistics, and application envelope. SDK protocol metadata, the JSON-RPC envelope, and error diagnostics are outside that cap. The server checks sizes before retaining paths and encoded values. If the limit is exceeded, the operation returns an error and discards the partial page. Use a smaller page or narrower query to reduce the result size.

The server conservatively reserves space for continuation metadata, so very small limits may be rejected before it can determine whether a page is terminal. Omit `max_result_bytes` to use the server default.

An implicit read closes its handle when it finishes. If it returns a cursor, it exposes `file_id` for cleanup. Cursors are opaque and single-use after a successful continuation. Caller cancellation and busy-handle rejection preserve a cursor; other failures can invalidate it. A cursor stores the query plan and consumed-match count, then rescans the source prefix for later pages. This keeps suspended cursor state small while making later pages slower; see [performance notes](docs/performance.md).

An explicit handle can serve independent queries. If a scan already holds its source lease, another read returns `RESOURCE_LIMIT_EXCEEDED` with `limit.name: "source_busy"` immediately.

### Close a handle

```json
{"file_id":"jf_..."}
```

`json_close` returns `{"file_id":"jf_...","closed":true}`. Repeating the operation returns `closed:false`. Closing invalidates associated cursors before it waits for an active read to release its lease. A cancelled caller stops waiting, but cleanup still finishes.

## Formats and query support

| Format | Behavior |
| --- | --- |
| `auto` | Detects from a bounded sample and file extension. |
| `json` | One standard JSON value; any JSON root type is supported. |
| `jsonl` | One value per physical LF/CRLF-delimited record. Interior blank lines are invalid; final LF is optional. Records form a virtual array. |
| `json-seq` | RFC 7464 record-separator framing. Repeated RS bytes introduce a record without incrementing its index. A top-level number requires trailing JSON whitespace. An empty or RS-only stream has zero records. |

Auto-detection selects JSON-seq when RS appears at byte zero, JSONL for `.jsonl` or `.ndjson` files, and JSONL for multiple independently valid line-aligned values. An ambiguous single value defaults to JSON. The chosen format stays fixed for the handle.

JSON Pointer accepts `/orders/0/id` and `#/orders/0/id`. The empty string selects the root; `~1` escapes `/` and `~0` escapes `~`.

JSONPath supports a bounded forward profile: child names, nonnegative indexes, wildcards, forward slices, and filters over singular paths. Filters support existence checks, scalar literals, comparisons, negation, `&&`, `||`, and parentheses. Queries can contain at most 64 selectors. Descendant selectors, unions, negative indexes, reverse or zero-step slices, functions, and deep object or array comparisons are not supported. See the [full JSONPath profile](docs/jsonpath-profile.md) for grammar and comparison rules.

Inputs must use valid UTF-8 and well-formed escaped surrogate pairs, and object member names must be unique. JSON comments, JSON5, compressed input, URLs, SQL, and jq are not supported. A malformed record fails the operation.

`complete: false` covers only the scanned prefix and lookahead. A successful terminal page completes validation. The server checks file identity, size, and modification time when opening and before each read, then again after execution while holding the source lease. These checks detect ordinary source changes; keep input files immutable for consistent results.

## Resource limits

Per-read `max_items` and `max_result_bytes` can lower the configured caps. Use `mcp-json-reader --help` for flag names.

| Limit | Default |
| --- | ---: |
| Source path / query bytes | 32 KiB / 16 KiB |
| Encoded bytes in one string token | 1 MiB |
| Number-token bytes | 1 KiB |
| Container depth | 256 |
| Members in one object | 100,000 |
| Decoded key bytes per object / active nested objects | 8 MiB / 32 MiB |
| JSONL or JSON-seq record bytes | 64 MiB |
| Matched or filter-candidate bytes | 8 MiB |
| Serialized structured read data | 4 MiB |
| Items per page | 1,000 |
| Request deadline, including scan admission | 30 seconds |
| Probe bytes / complete records | 4 MiB / 32 |
| Open handles / cursors / concurrent scans | 32 / 128 / 4 |
| Handle / cursor idle lifetime | 15 minutes / 5 minutes |
| MCP stdio frame | 16 MiB |

The stdio frame limit applies before tool dispatch. Each frame must contain one complete JSON object on one physical line; whitespace and the line delimiter count toward the limit. The frame buffer and SDK decoder allocations add to process memory, along with parser bookkeeping, buffers, Go objects, and concurrent scans. Use operating-system process limits for a hard memory ceiling. Deadline checks are cooperative, so an unresponsive filesystem call can exceed the request deadline.

For larger workloads, see [performance notes](docs/performance.md) for pagination costs, validation behavior, and measured scenarios.

## Errors

Application failures have `isError: true` and a structured error object in `structuredContent`, for example:

```json
{"code":"RESOURCE_LIMIT_EXCEEDED","message":"max_result_bytes exceeded","limit":{"name":"max_result_bytes","limit":1024}}
```

| Code | Meaning |
| --- | --- |
| `UNSUPPORTED_FORMAT` | Unsupported format in a direct service call. MCP schema violations use `INVALID_ARGUMENT`. |
| `FORMAT_MISMATCH` | Source framing differs from the selected format. |
| `SYNTAX_ERROR` | Malformed JSON, Unicode, duplicate names, or record framing. |
| `UNSUPPORTED_SYNTAX` | Recognized nonstandard JSON construct. |
| `QUERY_SYNTAX_ERROR` | Malformed query or an integer outside the interoperable domain. |
| `UNSUPPORTED_QUERY_FEATURE` | Query construct outside the documented profile. |
| `RESOURCE_LIMIT_EXCEEDED` | Parser, result, deadline, admission, handle, or cursor limit. |
| `SOURCE_CHANGED` | Source identity or fingerprint changed. |
| `HANDLE_EXPIRED` | Closed, consumed, expired, or unavailable handle or cursor. |
| `INVALID_ARGUMENT` | Arguments fail the declared closed schema or service contract. |
| `ACCESS_DENIED` | Confined file access was denied. |
| `IO_ERROR` | Source I/O failed. |
| `CANCELLED` | Caller cancellation was observed. |
| `INTERNAL_ERROR` | Internal operation failed. |

Message, hint, and diagnostic path text are each limited to 1,024 UTF-8 bytes at the MCP boundary. Input property names are case-sensitive, and duplicate argument properties are rejected before decoding.

Version 3 uses the Go module path `github.com/oovz/mcp-json-reader/v3`. It requires MCP 2026-07-28 and a client that reads structured tool results. See [support](SUPPORT.md) for platform details.
