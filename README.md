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
npx mcp-json-reader
```

## Tools

### `query`
Query a local JSON file using JSONPath syntax with extended operations.
- **Arguments**:
  - `path` (string): Absolute or relative path to the JSON file.
  - `jsonPath` (string): JSONPath expression (e.g., `$.store.book[*].author`).

### `filter`
Filter an array within a local JSON file using specific conditions.
- **Arguments**:
  - `path` (string): Path to the JSON file.
  - `jsonPath` (string): Path to the array to filter.
  - `condition` (string): Condition (e.g., `@.price > 10` or `@.title.contains('Lord')`).

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
      "args": ["-y", "mcp-json-reader"],
      "env": {
        "NODE_ENV": "production"
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