# MCP JSON Reader

[![CI](https://github.com/oovz/mcp-json-reader/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/oovz/mcp-json-reader/actions/workflows/ci.yml)
[![npm version](https://img.shields.io/npm/v/mcp-json-reader)](https://www.npmjs.com/package/mcp-json-reader)
[![Go Reference](https://pkg.go.dev/badge/github.com/oovz/mcp-json-reader/v3.svg)](https://pkg.go.dev/github.com/oovz/mcp-json-reader/v3)

A local [Model Context Protocol](https://modelcontextprotocol.io/) server that lets an MCP client query JSON, JSON Lines, and JSON-sequence files. It reads files incrementally and returns bounded pages of matching values and their paths.

Use [JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901.html) for a known location or a restricted [JSONPath](https://www.rfc-editor.org/rfc/rfc9535.html) profile to select records and fields. The server is written in Go and communicates over stdio.

| Tool | Purpose |
| --- | --- |
| `json_open` | Inspect a file and open a handle for repeated queries. |
| `json_read` | Query a path or handle, or continue a result page. |
| `json_close` | Release a handle and its pagination cursors. |

## Quick start

You need an MCP client that supports **MCP 2026-07-28** and reads `structuredContent` tool results. Older protocol versions are rejected. For npm, use Node.js **22.14.0 or later** and npm **11.5.1 or later**.

Add this server to your client's MCP configuration:

```json
{
  "mcpServers": {
    "json-reader": {
      "command": "npx",
      "args": ["--yes", "mcp-json-reader@3"]
    }
  }
}
```

The client starts the server as a subprocess. The npm package supplies the native Go executable; no Go installation is needed.

> [!IMPORTANT]
> By default, the server can read any regular file permitted by your operating-system account. Configure workspaces or use `--root` to restrict access to selected directories.

## Global use and workspaces

Install the command once and use it across projects. With no workspace configuration, absolute source paths can point anywhere your account has read access. Relative paths resolve from the server process's working directory, which the MCP client controls; use absolute paths in global client configurations.

To save a list of allowed workspaces, create a JSON file such as `/absolute/path/workspaces.json`:

```json
{
  "workspaces": ["/workspace/project-a", "/workspace/project-b"]
}
```

Start the server with that file:

```sh
mcp-json-reader --config /absolute/path/workspaces.json
```

The file is loaded only when `--config` is supplied. It must contain exactly one `workspaces` array and fit within 1 MiB. Workspace entries must be existing absolute directories. With multiple workspaces, source paths must be absolute; with one, relative paths resolve from that workspace. An empty list allows unrestricted access. Invalid configurations stop startup.

For one directory, use `--root /workspace/data` or set `MCP_JSON_ROOT`. An explicit `--root` overrides that environment variable. `--config` cannot be combined with either. A relative root such as `.` resolves from the process's working directory.

Workspace restrictions confine tool requests and symlink resolution to the configured directory trees. When configured workspaces overlap within one directory tree, reads use the containing workspace's access boundary. With `--root /workspace/data`:

| Requested path | Behavior |
| --- | --- |
| `orders.jsonl` | Reads `/workspace/data/orders.jsonl`. |
| `archive/orders.json` | Reads a file in a subdirectory of the root. |
| `/workspace/data/orders.jsonl` | Accepts an absolute path inside the root. |
| `../secrets.json` or `/other/data.json` | Rejects access outside the root. |

The server accepts regular files and does not restrict access by filename extension. Keep source files unchanged while handles are open. See [security guidance](SECURITY.md) for mount, hard-link, and file-mutation limits.

## Installation options

All installation methods run the Go server. Choose a standalone binary to run without a language runtime, npm for `npx` integration, or `go install` to compile it with your Go toolchain.

### Standalone binary

Download the binary for your version and platform from [GitHub Releases](https://github.com/oovz/mcp-json-reader/releases). Files are named `mcp-json-reader_<version>_<os>_<arch>`, with `.exe` on Windows. Linux, macOS (`darwin`), and Windows are available on amd64 and arm64. Release assets include `SHA256SUMS` for verification and `LICENSE` with third-party notices.

On Linux and macOS, make the downloaded file executable with `chmod +x`. Rename it to `mcp-json-reader` (or `mcp-json-reader.exe` on Windows), then put it on your `PATH` or use its absolute path in your MCP client configuration. The executable runs without Go, Node.js, npm, or Python installed:

```sh
mcp-json-reader --version
mcp-json-reader
```

### npm

Run without a persistent global installation:

```sh
npx --yes mcp-json-reader@3
```

Or install the command:

```sh
npm install --global mcp-json-reader
mcp-json-reader
```

The package includes binaries for Linux, macOS, and Windows on amd64 and arm64. Its JavaScript launcher selects the matching binary and forwards arguments, standard streams, signals, and exit status. It has no install scripts and does not download a binary at startup. Node.js and npm are required for this installation method.

### Go

Use Go **1.27.2 or later**:

```sh
go install github.com/oovz/mcp-json-reader/v3/cmd/mcp-json-reader@latest
mcp-json-reader
```

Go installs the executable in `GOBIN`, or `$(go env GOPATH)/bin` by default. Add that directory to your `PATH`, or use the executable's absolute path in your client configuration. The installed binary runs without Node.js or npm.

### Build this checkout

```sh
go build -trimpath -o bin/mcp-json-reader ./cmd/mcp-json-reader
./bin/mcp-json-reader
```

On Windows, build an `.exe` and use absolute executable paths. Builds can use `CGO_ENABLED=0`. See [supported platforms](SUPPORT.md) for operating-system requirements.

## Configure an MCP client

For a globally installed command, replace the example executable path with your own:

```json
{
  "mcpServers": {
    "json-reader": {
      "command": "/absolute/path/to/mcp-json-reader"
    }
  }
}
```

To apply a saved workspace configuration, add:

```json
{"args": ["--config", "/absolute/path/workspaces.json"]}
```

To restrict the server to one directory through the environment:

```json
{
  "mcpServers": {
    "json-reader": {
      "command": "/absolute/path/to/mcp-json-reader",
      "env": {"MCP_JSON_ROOT": "/workspace/data"}
    }
  }
}
```

Clients must discover the server through `server/discover` and read result values and application errors from `structuredContent`; the `content` array is empty. Required metadata is checked before SDK dispatch. Missing or malformed metadata returns JSON-RPC `-32602`; an unsupported protocol version returns `-32022`.

## Query a file

For a file at `/workspace/data/orders.jsonl`, call `json_read` with:

```json
{"path":"/workspace/data/orders.jsonl","format":"jsonl","language":"jsonpath","query":"$[*].id","max_items":2}
```

JSONL and JSON-sequence records form a virtual array, so `$[*].id` selects each record's `id`. Each result item contains a JSON Pointer path, such as `/0/id`, and its value. If the response contains `next_cursor`, pass it to `json_read`:

```json
{"cursor":"jc_..."}
```

Use a Pointer for one known value:

```json
{"path":"/workspace/data/orders.jsonl","format":"jsonl","language":"pointer","query":"/0/id"}
```

JSONPath supports child names, nonnegative indexes, wildcards, forward slices, and filters such as `$[?@.total >= 100].id`. Descendant selectors, unions, negative indexes, reverse slices, and functions are outside the supported profile. See the [query profile](docs/jsonpath-profile.md) for the grammar and comparison rules.

Input must be standard JSON with valid Unicode and unique object member names. JSON comments, JSON5, compressed files, URLs, SQL, and jq are unsupported. A malformed record fails the operation.

## Limits and larger files

Defaults include 1,000 items and 4 MiB of structured read data per page, a 30-second request deadline, and four concurrent scans. Limits also apply to tokens, nesting, records, filter candidates, handles, and cursors. Use `mcp-json-reader --help` for flags and the [tool reference](docs/tools.md#resource-limits) for all defaults.

Later pages rescan the source prefix. Prefer narrow queries to long pagination chains; full-file export and global aggregation are outside this interface. A partial page validates only the scanned prefix and lookahead. Deadlines are cooperative, and process memory includes parser and protocol overhead. See [performance notes](docs/performance.md) for the resource model and measured scenarios.

## Documentation

- [Tool reference](docs/tools.md): arguments, responses, formats, pagination, limits, and errors.
- [JSONPath profile](docs/jsonpath-profile.md): supported selectors and filter semantics.
- [Performance](docs/performance.md): pagination costs, validation, and memory use.
- [Support](SUPPORT.md) and [security guidance](SECURITY.md): platform requirements and operating boundaries.
- [GitHub Releases](https://github.com/oovz/mcp-json-reader/releases): standalone binaries, checksums, version notes, and commits between releases.
