# MCP JSON Reader

A local stdio [Model Context Protocol](https://modelcontextprotocol.io/) server for querying JSON documents, JSON Lines, and JSON sequences. It reads incrementally and limits token size, nesting, filter candidates, result data, and concurrent scans.

Queries use exact [JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901.html) paths or a restricted forward-streaming [JSONPath](https://www.rfc-editor.org/rfc/rfc9535.html) profile. The three tools are `json_open`, `json_read`, and `json_close`.

Version 3 requires an MCP 2026-07-28 client that reads structured tool results.

## Install

With Go **1.27.1 or later**:

```sh
go install github.com/oovz/mcp-json-reader/v3/cmd/mcp-json-reader@latest
mcp-json-reader --root /workspace/data
```

Go installs the executable in `GOBIN`, or `$(go env GOPATH)/bin` by default.
Add that directory to your `PATH`.

With Node.js **22.14.0 or later** and npm **11.5.1 or later**:

```sh
npx --yes @oovz/mcp-json-reader@3 --root /workspace/data
```

For a persistent npm installation:

```sh
npm install --global @oovz/mcp-json-reader
mcp-json-reader --root /workspace/data
```

The server is written in Go. The npm package includes Go binaries for Linux,
macOS, and Windows on amd64 and arm64. Its small JavaScript launcher selects
the binary and forwards standard streams, arguments, signals, and exit status.
Go installations run the executable directly.

## Build and run

Build this checkout with Go 1.27.1:

```sh
go build -trimpath -o bin/mcp-json-reader ./cmd/mcp-json-reader
./bin/mcp-json-reader --root /workspace/data
```

On Windows, build an `.exe` and use absolute executable and root paths.
Builds can use `CGO_ENABLED=0`. See [SUPPORT.md](SUPPORT.md) for supported platforms.

Every requested source must resolve beneath the configured root. `MCP_JSON_ROOT` supplies the root when `--root` is omitted. Keep source files immutable while their handles are open. Use a dedicated, trusted data directory containing regular files; see [SECURITY.md](SECURITY.md).

Example client configuration:

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

The server supports MCP **2026-07-28** over stdio. It validates required request metadata before SDK dispatch. Clients use `server/discover` and read tool data from `structuredContent`; `content` is an empty array. Source handles and cursors belong to the server process and expire according to their idle limits.

## Tools

### `json_open`

Open a reusable handle and inspect a file:

```json
{"path":"orders.jsonl","format":"jsonl","validation":"probe"}
```

The response reports `file_id`, requested/detected format, validation coverage, and source size/modification time. `probe` examines a bounded prefix. For JSONL and JSON-seq, both the byte and record caps apply, including to small files. `validation.complete: false` means later bytes remain unvalidated. `full` streams the complete source. An explicit format is authoritative.

### `json_read`

After opening `orders.jsonl`, use its returned `file_id` to read the first
record's `id` with JSON Pointer:

```json
{"file_id":"jf_...","language":"pointer","query":"/0/id"}
```

Or open a path for one query:

```json
{"path":"orders.jsonl","format":"jsonl","language":"jsonpath","query":"$[*].id","max_items":2}
```

Each returned item contains an RFC 6901 path and a JSON value:

```json
{
  "file_id":"jf_...",
  "language":"jsonpath",
  "query":"$[*].id",
  "items":[{"path":"/0/id","value":101},{"path":"/1/id","value":102}],
  "complete":false,
  "next_cursor":"jc_...",
  "stats":{"returned":2,"skipped":0}
}
```

Continue using the cursor alone:

```json
{"cursor":"jc_..."}
```

Cursors are opaque and single-use after a successful continuation. Caller cancellation and busy-handle rejection preserve a cursor; other failures can invalidate it. They bind the source handle, compiled query, limits, and number of consumed matches. Continuation rescans the source prefix and skips consumed matches. Cursor state stays small at the cost of repeated reads; [performance measurements](docs/performance.md) show the page-position cost.

`max_items` determines page boundaries. `max_result_bytes` is a hard cap on the UTF-8 JSON encoding of the complete **`structuredContent` object**: values, escaped paths, query, cursor, handle, statistics, and application envelope. SDK-added protocol metadata, the outer JSON-RPC envelope, and error diagnostics are outside this application-data cap. The server checks sizes incrementally before allocating retained paths or encoded values. Budget exhaustion returns an error and discards the partial page. Use a smaller page or a narrower query to reduce result size.

The size preflight conservatively reserves continuation metadata. Very small budgets can be rejected before the server knows that a page is terminal. Omit the parameter to use the server default.

An implicit path read closes its handle when complete. During pagination its `file_id` is exposed for cleanup; continue using `next_cursor`. An explicit handle can serve independent queries. A handle already leased by a scan returns `RESOURCE_LIMIT_EXCEEDED` with `limit.name: "source_busy"` immediately.

Pointers accept plain `/orders/0/id` and URI-fragment `#/orders/0/id` forms. The empty string selects the root; `~1` escapes `/` and `~0` escapes `~`. The [JSONPath profile](docs/jsonpath-profile.md) defines supported selectors and comparisons.

### `json_close`

```json
{"file_id":"jf_..."}
```

Returns `{"file_id":"jf_...","closed":true}`. Repeating the operation returns `closed:false`. Closing invalidates associated cursors before waiting for an active lease. A cancelled caller stops waiting; cleanup finishes after the active read releases its lease.

## Formats and validation

| Format | Contract |
| --- | --- |
| `auto` | Detect from a bounded sample and extension. |
| `json` | Exactly one standard JSON value; every JSON root type is supported. |
| `jsonl` | Each physical LF/CRLF-delimited record contains one value. Interior blank lines are invalid. Final LF is optional. Records form a virtual array. |
| `json-seq` | RFC 7464 record-separator framing. A run of RS bytes introduces the next record. A top-level number requires trailing JSON whitespace. Records form a virtual array. An empty stream or an RS-only stream contains zero records. |

Auto-detection chooses JSON-seq for RS at byte zero, JSONL for `.jsonl`/`.ndjson`, and JSONL for multiple independently valid line-aligned values. Ambiguous single values default to JSON. A selected format is fixed for the handle.

The input policy requires valid UTF-8, well-formed escaped surrogate pairs, and unique object member names. JSON comments, JSON5, compressed input, URLs, SQL, and jq are outside the supported contract. A malformed record fails the operation.

`complete:false` covers only the scanned prefix and lookahead. A successful terminal page completes the scan and its validation. Identity, size, and modification time are checked at open, before each read, and after execution while the source lease is held. These checks detect ordinary source changes; immutable input remains required for consistent results.

## Resource limits

Per-read `max_items` and `max_result_bytes` can lower server caps. CLI flags configure the server limits.

| Limit | Default |
| --- | ---: |
| Source path / query bytes | 32 KiB / 16 KiB |
| Encoded bytes inside one string token | 1 MiB |
| Number-token bytes | 1 KiB |
| Container depth | 256 |
| Members in one object | 100,000 |
| Decoded key bytes per object / active nested objects | 8 MiB / 32 MiB |
| JSONL or JSON-seq record bytes | 64 MiB |
| Matched/filter candidate bytes | 8 MiB |
| Serialized structured read data | 4 MiB |
| Items per page | 1,000 |
| Request deadline, including scan admission | 30 seconds |
| Probe bytes / complete records | 4 MiB / 32 |
| Open handles / cursors / concurrent scans | 32 / 128 / 4 |
| Handle / cursor idle lifetime | 15 minutes / 5 minutes |
| MCP stdio frame | 16 MiB |

Run `mcp-json-reader --help` for flag names. Process memory also includes parser
bookkeeping, buffers, Go objects and concurrent scans. Use OS process limits
for a hard memory ceiling. Deadline checks are cooperative around parsing and
reads; an unresponsive filesystem call can exceed the deadline.

The 16 MiB stdio frame cap is applied before a request reaches the MCP tool
handlers. Each frame must contain one complete JSON object on one physical
line. The cap includes whitespace and the line delimiter. The frame buffer and
SDK decoder allocations add to process memory.

Missing or malformed required protocol metadata returns JSON-RPC `-32602`.
An unsupported protocol version returns `-32022`. Clients can correct the
request and continue using the same connection.

## Errors

Application failures have `isError:true` and a structured error object:

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
| `HANDLE_EXPIRED` | Closed, consumed, expired, or unavailable handle/cursor. |
| `INVALID_ARGUMENT` | Arguments fail the declared closed schema or service contract. |
| `ACCESS_DENIED` | Confined file access was denied. |
| `IO_ERROR` | Source I/O failed. |
| `CANCELLED` | Caller cancellation was observed. |
| `INTERNAL_ERROR` | Internal operation failed. |

Message, hint, and diagnostic path text are bounded individually to 1,024 UTF-8 bytes at the MCP boundary. Input property names are case-sensitive. Duplicate argument properties are rejected before decoding can collapse them.
