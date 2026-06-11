# MCP JSON Reader (`mcp-json-reader`)

[![npm version](https://img.shields.io/npm/v/mcp-json-reader.svg)](https://www.npmjs.com/package/mcp-json-reader)

A Model Context Protocol (MCP) server for reading and querying **local** JSON files with extended syntax. This tool enables LLMs to perform complex data manipulations directly on local JSON datasets.

## Features

- **Local File Support**: Read any local JSON file.
- **Extended JSONPath**: Support for standard JSONPath plus:
  - **Sorting**: `.sort(field)`, `.sort(-field)`
  - **Aggregation**: `.sum(field)`, `.avg(field)`, `.min(field)`, `.max(field)`
  - **Numeric**: `.math(+10)`, `.round()`, `.abs()`, etc.
  - **String**: `.contains()`, `.startsWith()`, `.toLowerCase()`, etc.
  - **Date**: `.format('YYYY-MM-DD')`, `.isToday()`
  - **Array**: `.distinct()`, `.reverse()`, `[start:end]`
- **Filtering**: Powerful filtering tool for array data.

## Installation

```bash
# Install globally
npm install -g .

# Or run with npx
npx mcp-json-reader --root /path/to/data
```

## Configuration

The server supports an **optional** base directory for resolving relative paths.

- **Command Line**: `--root <base_path>` (Optional)
- **Environment Variable**: `MCP_JSON_ROOT` (Optional)

If neither is provided, the server defaults to the Current Working Directory (CWD) of the process. This is useful for IDEs like VSCode or Cursor, where the root path can be set per-workspace.

## Performance & Memory Limits

### Caching

The server implements an in-memory **LRU cache** (up to 10 entries) for parsed JSON objects. Subsequent queries on the same file are extremely fast as they skip the read and parse steps. The cache automatically detects file changes using modification timestamps and invalidates stale entries.

### V8 Heap Memory Limitation

This server runs on Node.js, which uses the V8 JavaScript engine. V8 imposes a default heap memory limit of approximately **4 GB** on 64-bit systems. This creates a hard upper bound on the size of JSON files that can be safely loaded and queried.

**Why this matters:**

- `JSON.parse()` builds a full in-memory object graph that typically consumes **2–6× the raw file size** in heap memory. A 1 GB JSON file may require 2–6 GB of heap just for the parsed representation.
- The in-memory cache retains parsed objects for fast re-query, multiplying memory usage by the number of cached files.
- Query results (sorting, filtering, aggregation) create additional temporary allocations.

**Default safe limit:** The server enforces a maximum file size of **1.5 GB** per file (configurable). On a **16 GB system** with ~4 GB available to V8:

| Raw File Size | Estimated Heap Usage (parsed) | Fits in 4 GB V8 Heap? |
|--------------|-------------------------------|----------------------|
| 100 MB       | 200–600 MB                    | Yes                  |
| 500 MB       | 1–3 GB                        | Usually              |
| 1 GB         | 2–6 GB                        | Risky                |
| 1.5 GB       | 3–9 GB                        | At the edge          |
| 2+ GB        | 4–12 GB                       | No — OOM crash       |

**Tuning the limits:**

```bash
# Increase V8 heap (e.g., to 8 GB)
node --max-old-space-size=8192 ./build/index.js

# Increase the file size limit via environment variable (in MB)
MCP_MAX_FILE_SIZE_MB=2048 npx mcp-json-reader
```

**Recommendations for large files:**

- For files > 500 MB, monitor memory usage with `process.memoryUsage()`.
- For files > 1 GB, increase `--max-old-space-size` proportionally and set `MCP_MAX_FILE_SIZE_MB`.
- For files > 2 GB, consider pre-processing (splitting, filtering) before loading into this server.
- The cache holds up to 10 files simultaneously — if working with several large files, each contributes to total heap pressure.
## Tools

### `query`
Query a local JSON file using standard JSONPath with custom extensions for data manipulation.
- **Arguments**:
  - `path` (string): Absolute path or path relative to the configured root directory.
  - `jsonPath` (string): JSONPath expression (e.g., `$.store.book[*].author`). Supports extensions like `.sort()`, `.sum()`, `.math()`, etc.

### `filter`
Extract and filter elements from an array within a local JSON file using advanced logic.
- **Arguments**:
  - `path` (string): Absolute path or path relative to the configured root directory.
  - `jsonPath` (string): JSONPath to the array to filter (e.g., `$.store.book`).
  - `condition` (string): Filter condition (e.g., `@.price > 10` or `@.title.contains('Lord')`).

## Examples

### Query with Sorting and Slicing
```json
{
  "path": "./data.json",
  "jsonPath": "$.items.sort(-price)[0:5]"
}
```

### Aggregation
```json
{
  "path": "./sales.json",
  "jsonPath": "$.transactions.sum(amount)"
}
```

### Complex Filtering
```json
{
  "path": "./users.json",
  "jsonPath": "$.users",
  "condition": "@.email.endsWith('@gmail.com')"
}
```

## Configuration for Claude Desktop

Add this to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "json-reader": {
      "command": "npx",
      "args": ["-y", "mcp-json-reader", "--root", "/absolute/path/to/your/json/data"],
      "env": {
        "MCP_JSON_ROOT": "/optional/env/path"
      }
    }
  }
}
```

## Development

```bash
npm install
npm run build
npm test
```

## License

MIT