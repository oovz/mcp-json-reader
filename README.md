# MCP JSON Reader

A bounded-memory [Model Context Protocol](https://modelcontextprotocol.io/) server for querying large or complex local JSON files.

MCP JSON Reader streams tokens from disk instead of unmarshaling the whole file. It supports exact [JSON Pointer](https://datatracker.ietf.org/doc/html/rfc6901) paths, a documented forward-streaming [JSONPath](https://datatracker.ietf.org/doc/html/rfc9535) profile, reusable file handles, and bounded pagination over local stdio.

> [!IMPORTANT]
> Version 2 is a breaking Go rewrite. The former npm package, JSON5 support, `query`/`filter` tools, and proprietary query operations are gone.

## Features

- Streams JSON, JSONL, and JSON-seq files of any size with fixed memory caps
- Exact JSON Pointer and forward-streaming JSONPath queries
- Reusable process-scoped file handles with cursor pagination
- `os.Root` path confinement; no network transport or URL input
- Structured, machine-readable tool errors

> [!NOTE]
> JSONC and JSON5 support is planned for a future release. Compressed input, SQL, jq, and network URLs are out of scope.

## Install

Requires Go 1.26.5 or later.

```bash
go install github.com/oovz/mcp-json-reader/v2/cmd/mcp-json-reader@v2.0.1
```

From an untagged checkout:

```bash
CGO_ENABLED=0 go build -trimpath -o bin/mcp-json-reader ./cmd/mcp-json-reader
```

Pick a root directory. Every requested file must stay inside it, including after symlink resolution.

```bash
mcp-json-reader --root /workspace/data
```

`MCP_JSON_ROOT` works in place of `--root`. On Windows, use absolute paths for both the executable and the root when the client does not inherit your `PATH`.

Register the server with an MCP client:

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

<details>
<summary><strong>For AI agents</strong></summary>

This server lets a coding agent read local JSON, JSONL, and JSON-seq files without loading them into memory. Hand it a root directory and call three tools: `json_open`, `json_read`, `json_close`.

Open a file and get a handle:

```json
{ "path": "orders.jsonl", "format": "auto", "validation": "probe" }
```

Read by handle…

```json
{ "file_id": "jf_...", "language": "pointer", "query": "/orders/0/id" }
```

…or open a path implicitly and query in one call:

```json
{ "path": "orders.jsonl", "format": "jsonl", "language": "jsonpath", "query": "$[*].id", "max_items": 100 }
```

Page through large results by passing the cursor returned in the previous response:

```json
{ "cursor": "jc_..." }
```

Close the handle when done (close is idempotent):

```json
{ "file_id": "jf_..." }
```

See the reference below for tool schemas, formats, limits, and error codes.

</details>

## Tools

### `json_open`

Open and inspect a file without materializing it. Returns a process-scoped handle plus the fixed format, validation coverage, and source metadata.

```json
{ "path": "orders.jsonl", "format": "auto", "validation": "probe" }
```

`validation` has two modes: `probe` examines a bounded prefix (large files return `complete: false` and can still fail later), `full` streams and validates the complete file without materializing it. Explicit formats are authoritative—a file opened as `json` is never silently reinterpreted as JSONL.

### `json_read`

Read from a handle, or open a path implicitly and query in one call. Matches contain an RFC 6901 path and a JSON value:

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

Continue with the opaque cursor by itself: `{"cursor": "jc_..."}`. Cursors are single-use, process-scoped, bound to the source fingerprint and original query, and expire after inactivity. Each continuation rescans from the beginning and skips previously returned matches, keeping cursor state small at the cost of repeated I/O.

Pagination is driven by `max_items`. `max_result_bytes` is a hard limit on the complete serialized response; it does not split a value or create a page automatically. If the envelope exceeds the limit, lower `max_items` or raise the server cap.

An implicit read closes its hidden file handle when the result is complete. While paginated, the response includes a `file_id` only for cleanup via `json_close`; continue with `next_cursor`, not by starting another read from that `file_id`.

Pointers accept plain (`/orders/0/id`) and URI-fragment (`#/orders/0/id`) forms. Use `~1` for `/` and `~0` for `~` inside a member name. See the [JSONPath profile](docs/jsonpath-profile.md) for supported selectors, filter semantics, and deliberate limits.

### `json_close`

```json
{ "file_id": "jf_..." }
```

Returns `{"file_id":"jf_...","closed":true}`. Repeating it returns the same `file_id` with `closed: false`. Cancellation stops waiting for `json_close`, but a close that has started is not rolled back: the handle remains unavailable and cleanup finishes after any active read releases its lease.

## Formats

| Value | Meaning |
| --- | --- |
| `auto` | Detect from a bounded sample and extension. Never guesses JSONC or JSON5. |
| `json` | Exactly one standard JSON value. Any JSON root type is valid. |
| `jsonl` | Every physical LF/CRLF-delimited record must contain exactly one standard JSON value. Interior blank lines are invalid; final LF is optional. Records form a virtual array. |
| `json-seq` | Zero or more values prefixed by ASCII RS `0x1E`. Consecutive separators create an invalid empty record. A top-level number must be followed by JSON whitespace. Records form a virtual array; the empty byte stream is valid. |

Auto-detection rules: an RS at byte zero identifies `json-seq`; `.jsonl` and `.ndjson` identify `jsonl`; multiple independently valid line-aligned values identify `jsonl`; ambiguous one-line values and unknown extensions default to `json`. Once selected, a handle's format never changes.

<details>
<summary><strong>Resource limits</strong></summary>

Defaults can be changed with CLI flags. Per-read `max_items` and `max_result_bytes` may only lower server caps.

| Limit | Default |
| --- | ---: |
| Path bytes | 32 KiB |
| Query bytes | 16 KiB |
| Encoded bytes inside one string token | 1 MiB |
| Number bytes | 1 KiB |
| Nesting depth | 256 |
| Members per object | 100,000 |
| Retained key bytes per object | 8 MiB |
| Retained key bytes across active nested objects | 32 MiB |
| Record bytes | 64 MiB |
| Candidate bytes | 8 MiB |
| Serialized read result | 4 MiB |
| Items per page | 1,000 |
| Scan time | 30 seconds |
| Probe prefix | 4 MiB / 32 records |
| Open handles | 32 |
| Stored cursors | 128 |
| Concurrent scans | 4 |
| Handle / cursor idle TTL | 15 minutes / 5 minutes |

Run `mcp-json-reader -help` for the corresponding flags.

</details>

<details>
<summary><strong>Errors</strong></summary>

Tool failures use `isError: true`. The same machine-readable object appears in `structuredContent` and as JSON text for older clients.

```json
{
  "code": "FORMAT_MISMATCH",
  "message": "expected one JSON document, but found another top-level value",
  "expected_format": "json",
  "likely_formats": ["jsonl"],
  "retry": {"format": "jsonl"}
}
```

| Code | Meaning |
| --- | --- |
| `UNSUPPORTED_FORMAT` | The requested format name is not supported. |
| `FORMAT_MISMATCH` | The selected framing does not match the observed source. |
| `SYNTAX_ERROR` | A standard JSON value or frame is malformed. |
| `UNSUPPORTED_SYNTAX` | A recognized non-standard construct, currently JSON comments, was found. |
| `QUERY_SYNTAX_ERROR` | A Pointer or JSONPath expression is malformed. |
| `UNSUPPORTED_QUERY_FEATURE` | A Pointer or JSONPath feature is outside this server's profile. |
| `RESOURCE_LIMIT_EXCEEDED` | A configured parser, result, time, handle, cursor, or concurrency limit was reached. |
| `SOURCE_CHANGED` | File identity, size, or modification time changed after open. |
| `HANDLE_EXPIRED` | A handle or cursor is closed, expired, consumed, or otherwise unavailable. |
| `INVALID_ARGUMENT` | Tool fields are missing, conflicting, empty, or outside their allowed range. |
| `ACCESS_DENIED` | The requested path is outside the configured root or escapes through a link. |
| `IO_ERROR` | The source or configured root could not be read. |
| `CANCELLED` | The client cancelled the operation. |
| `INTERNAL_ERROR` | The server could not complete an internal operation. |

</details>

## Development

### Prerequisites

- Go 1.26.5 or later (`go version`)
- A C compiler (GCC, Clang, or MSVC) is required only for the race detector (`go test -race`)

### Get the source

```bash
git clone https://github.com/oovz/mcp-json-reader.git
cd mcp-json-reader
go mod download
```

### Build

```bash
go build -trimpath ./cmd/mcp-json-reader
# or a static binary with no CGO runtime requirement:
CGO_ENABLED=0 go build -trimpath -o bin/mcp-json-reader ./cmd/mcp-json-reader
```

### Run locally

```bash
./bin/mcp-json-reader --root ./tests
```

The server speaks MCP over stdio. Point any MCP client at the built binary with `--root` set to a directory of JSON files. The `tests/` folder ships fixtures (`mock.json`, `mock-large.json`, `mock-edge-cases.json`) you can query immediately.

### Test

```bash
# formatting and vet
gofmt -l cmd internal
go vet ./...

# full unit and integration suite
go test ./...
# run cases in a random order to catch order-dependent state
go test -shuffle=on ./...
# race detector (requires CGO and a C compiler)
go test -race ./...
```

### Fuzz

Seeded fuzz targets cover Pointer and JSONPath compilation, strict document validation, and both record framers. The normal test suite runs their seed corpus; you can also run each target for a bounded interval:

```bash
go test ./internal/query -run=^$ -fuzz=FuzzCompileQueries -fuzztime=10s
go test ./internal/stream -run=^$ -fuzz=FuzzValidateDocument -fuzztime=10s
go test ./internal/stream -run=^$ -fuzz=FuzzRecordFramers -fuzztime=10s
```

### What CI runs

CI runs on Ubuntu, Windows, and macOS with Go 1.26.x. The checks are `go mod verify`, `go mod tidy -diff`, `gofmt -l`, `go vet`, `go test ./... -count=1`, `go build -trimpath ./cmd/mcp-json-reader`, plus a separate race-detector job (`go test -race ./... -count=1`). Run the same commands locally before pushing.

## Standards

- MCP transport and tools: [MCP 2025-11-25](https://modelcontextprotocol.io/specification/2025-11-25); the pinned Go SDK also negotiates `2025-06-18`, `2025-03-26`, and `2024-11-05`
- Standard JSON: [RFC 8259](https://datatracker.ietf.org/doc/html/rfc8259), with invalid UTF-8 and duplicate names rejected
- JSON Pointer: [RFC 6901](https://datatracker.ietf.org/doc/html/rfc6901)
- JSONPath: [RFC 9535](https://datatracker.ietf.org/doc/html/rfc9535), documented streaming profile
- JSON text sequences: [RFC 7464](https://datatracker.ietf.org/doc/html/rfc7464)
