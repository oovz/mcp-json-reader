# MCP JSON Reader (`mcp-json-reader`)

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

## Performance

- **Caching**: The server implements an in-memory cache for parsed JSON objects. Subsequent queries on the same file are extremely fast as they skip the read and parse steps.
- **Cache Validation**: It automatically detects file changes using modification timestamps and invalidates the cache when necessary.

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
        "NODE_ENV": "production",
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