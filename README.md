# MCP JSON Reader (`mcp-json-reader`)

[![npm version](https://img.shields.io/npm/v/mcp-json-reader.svg)](https://www.npmjs.com/package/mcp-json-reader)
[![Install in VS Code](https://img.shields.io/badge/Install%20in-VS%20Code-007ACC?style=flat-square&logo=visual-studio-code&logoColor=white)](https://vscode.dev/redirect/mcp/install?name=mcp-json-reader&config=%7B%22command%22%3A%22npx%22%2C%22args%22%3A%5B%22-y%22%2C%22mcp-json-reader%22%5D%7D)
[![Install in VS Code Insiders](https://img.shields.io/badge/Install%20in-VS%20Code%20Insiders-24bfa5?style=flat-square&logo=visual-studio-code&logoColor=white)](https://insiders.vscode.dev/redirect/mcp/install?name=mcp-json-reader&config=%7B%22command%22%3A%22npx%22%2C%22args%22%3A%5B%22-y%22%2C%22mcp-json-reader%22%5D%7D&quality=insiders)
[![Install in Cursor](https://img.shields.io/badge/Install%20in-Cursor-000000?style=flat-square)](https://cursor.com/en/install-mcp?name=mcp-json-reader&config=eyJjb21tYW5kIjoibnB4IiwiYXJncyI6WyIteSIsIm1jcC1qc29uLXJlYWRlciJdfQ==)

A Model Context Protocol (MCP) server for reading, querying, and filtering **local** JSON files using extended JSONPath syntax. It allows LLMs to perform complex sorting, aggregations, math, and string operations directly on local datasets.

> [!NOTE]
> In addition to strict JSON (RFC 8259), the server parses **JSON5** — a superset that adds `//` and `/* */` comments, trailing commas, single-quoted strings, unquoted keys, hexadecimal numbers, `Infinity`/`-Infinity`/`NaN`, and multi-line strings. This makes it suitable for reading `tsconfig.json`-style JSONC files, commented config files, and other "JSON with comments" formats. See [spec.json5.org](https://spec.json5.org/).

## Quick Start

Run the server directly via `npx`:

```bash
npx mcp-json-reader --root /path/to/your/json/data
```

## Tools Exposed

| Tool | Description | Key Arguments |
| :--- | :--- | :--- |
| `query` | Queries local JSON using standard JSONPath + custom extensions (sorting, math, aggregates, etc.). | `path` (string), `jsonPath` (string) |
| `filter` | Extracts and filters elements from an array in a local JSON file using advanced logic. | `path` (string), `jsonPath` (string), `condition` (string) |

### Example Queries

*   **Sort & Slice:** `$.items.sort(-price)[0:5]` (Sort items by price descending and return top 5)
*   **Aggregation:** `$.transactions.sum(amount)` (Sum transaction amounts)
*   **Complex Filter:** `$.users` with condition `@.email.endsWith('@gmail.com')`

---

<details>
<summary>🛠️ IDE & Agent Configuration (Cursor, VS Code, Windsurf, Claude Desktop, Cline)</summary>

### Command Line Options & Env Variables
*   **Command Line**: `--root <base_path>` (Optional)
*   **Environment Variable**: `MCP_JSON_ROOT` (Optional)

If neither is provided, the server defaults to the Current Working Directory (CWD) of the process.

### Configuration Snippets

#### Claude Desktop
Add this to your `claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "mcp-json-reader": {
      "command": "npx",
      "args": ["-y", "mcp-json-reader", "--root", "/absolute/path/to/your/json/data"]
    }
  }
}
```

#### Cursor
Go to **Settings > Features > MCP**, click **Add New MCP Server**:
*   **Name**: `mcp-json-reader`
*   **Type**: `command`
*   **Command**: `npx -y mcp-json-reader --root /absolute/path/to/your/json/data`

#### Cline / Roo-Code
Add this to your `cline_mcp_settings.json` (or `roo_mcp_settings.json`):
```json
{
  "mcpServers": {
    "mcp-json-reader": {
      "command": "npx",
      "args": ["-y", "mcp-json-reader", "--root", "/absolute/path/to/your/json/data"]
    }
  }
}
```

#### Windsurf
Add this to your `mcp_config.json`:
```json
{
  "mcpServers": {
    "mcp-json-reader": {
      "command": "npx",
      "args": ["-y", "mcp-json-reader", "--root", "/absolute/path/to/your/json/data"]
    }
  }
}
```

#### GitHub Copilot (Coding Agent / CLI)
For the Copilot agent configuration:
```json
{
  "mcpServers": {
    "mcp-json-reader": {
      "command": "npx",
      "args": ["-y", "mcp-json-reader", "--root", "/absolute/path/to/your/json/data"],
      "tools": ["*"]
    }
  }
}
```
</details>

<details>
<summary>📝 Extended JSONPath Syntax Details</summary>

The query tool supports standard JSONPath plus the following custom extensions:

*   **Sorting**: `.sort(field)` (ascending) or `.sort(-field)` (descending)
*   **Aggregation**: `.sum(field)`, `.avg(field)`, `.min(field)`, `.max(field)`
*   **Numeric Ops**: `.math(+10)`, `.math(*2)`, `.round()`, `.abs()`, `.sqrt()`, etc.
*   **String Ops**: `.contains('x')`, `.startsWith('x')`, `.toLowerCase()`, `.toUpperCase()`, etc.
*   **Date Ops**: `.format('YYYY-MM-DD')`, `.isToday()`
*   **Array Ops**: `.distinct()`, `.reverse()`, `[start:end]` (slice)
</details>

<details>
<summary>⚡ Performance, Caching & V8 Memory Limits</summary>

### Caching
The server implements an in-memory **LRU cache** (up to 10 entries) for parsed JSON objects. Subsequent queries on the same file skip the read and parse steps. The cache automatically detects file modifications using file timestamps and invalidates stale entries.

### V8 Heap Memory Limits
Node.js imposes a default heap memory limit of ~4 GB on 64-bit systems. Parsing JSON constructs in-memory object graphs that consume **2–6× the raw file size**.

| Raw File Size | Estimated Heap Usage (parsed) | Fits in 4 GB V8 Heap? |
|--------------|-------------------------------|----------------------|
| 100 MB       | 200–600 MB                    | Yes                  |
| 500 MB       | 1–3 GB                        | Usually              |
| 1 GB         | 2–6 GB                        | Risky                |
| 1.5 GB       | 3–9 GB                        | At the edge          |
| 2+ GB        | 4–12 GB                       | No — OOM crash       |

> [!IMPORTANT]
> The default enforced file size limit is **1.5 GB** per file.

To tune limits for larger files:
```bash
# Increase V8 heap (e.g., to 8 GB)
node --max-old-space-size=8192 ./build/index.js

# Increase the file size limit via environment variable (in MB)
MCP_MAX_FILE_SIZE_MB=2048 npx mcp-json-reader
```
</details>

<details>
<summary>🤖 LLM / AI Model Quick-Start & Auto-Installation</summary>

> [!NOTE]
> This section is optimized for AI assistants reading this README to understand how to auto-configure and use this server.

### Server Identity
*   **Name**: `mcp-json-reader`
*   **Primary command**: `npx -y mcp-json-reader`
*   **Input Schema & Tools**:
    1.  `query` (reads and queries JSONPath)
        *   `path` (string): Absolute or relative file path.
        *   `jsonPath` (string): Extended JSONPath expression.
    2.  `filter` (filters array datasets)
        *   `path` (string): Absolute or relative file path.
        *   `jsonPath` (string): JSONPath pointing to an array.
        *   `condition` (string): Condition expression (e.g., `@.price > 10`).

### AI Instructions for Installation
If the user asks you (the AI model) to install or configure this MCP server:
1.  Locate their IDE's MCP configuration file (e.g., `claude_desktop_config.json` for Claude Desktop, `cline_mcp_settings.json` for Cline, or `mcp_config.json` for Windsurf).
2.  Add `mcp-json-reader` under the `mcpServers` object using:
    *   Command: `"npx"`
    *   Args: `["-y", "mcp-json-reader", "--root", "<project_root_or_data_directory>"]`
3.  Ensure the `--root` argument matches the workspace or data directory they intend to query.
</details>

## Development

```bash
npm install
npm run build
npm test
```