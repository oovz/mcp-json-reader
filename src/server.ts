import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";
import { JSONPath as JSONPathPlus } from 'jsonpath-plus';
import { JsonValue } from "./types.js";
import { readJsonFile, getRootPath } from "./cache.js";
import {
    isRecord,
    handleAggregation,
    handleNumericOperations,
    handleDateOperations,
    handleArrayOperations,
    handleStringOperations,
    handleComplexFilter
} from "./operations.js";

// Keep this in sync with package.json "version".
const SERVER_VERSION = "2.0.0";

export const server = new McpServer({
    name: "mcp-json-reader",
    version: SERVER_VERSION,
});

const QueryArgumentsSchema = z.object({
    path: z.string().describe("Absolute path or path relative to the configured root directory"),
    jsonPath: z.string().describe("JSONPath expression (e.g. '$.store.book[*].author'). Can include extensions like '.sort(field)', '.sum(field)', '.math(*2)', etc."),
});

const FilterArgumentsSchema = z.object({
    path: z.string().describe("Absolute path or path relative to the configured root directory"),
    jsonPath: z.string().describe("JSONPath to the target array (e.g. '$.store.book')"),
    condition: z.string().describe("Filter condition. Examples: '@.price > 10', '@.category == \"fiction\"', '@.title.contains(\"Lord\")'"),
});

/**
 * Resolves an extended JSONPath expression against parsed JSON data.
 * Dispatches to specialized handlers for aggregation, math, date, array,
 * and string operations; falls back to standard JSONPath otherwise.
 */
export function resolveQuery(jsonData: JsonValue, jsonPath: string): JsonValue {
    if (jsonPath === "$.length()") {
        return Array.isArray(jsonData)
            ? jsonData.length
            : (isRecord(jsonData) ? Object.keys(jsonData).length : 0);
    }

    if (jsonPath.match(/\.(sum|avg|min|max)\(/)) {
        const basePath = jsonPath.split(/\.(?:sum|avg|min|max)\(/)[0];
        const dataToAgg = basePath === '$'
            ? (Array.isArray(jsonData) ? jsonData : [jsonData])
            : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
        return handleAggregation(
            Array.isArray(dataToAgg) ? dataToAgg : [],
            jsonPath.slice(basePath.length)
        );
    }

    if (jsonPath.match(/\.(math|round|floor|ceil|abs|sqrt|pow2)\(/)) {
        const basePath = jsonPath.split(/\.(?:math|round|floor|ceil|abs|sqrt|pow2)\(/)[0];
        const dataToMath = basePath === '$'
            ? (Array.isArray(jsonData) ? jsonData : [jsonData])
            : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
        return handleNumericOperations(
            Array.isArray(dataToMath) ? dataToMath : [dataToMath],
            jsonPath.slice(basePath.length)
        );
    }

    if (jsonPath.match(/\.(format|isToday)\(/)) {
        const basePath = jsonPath.split(/\.(?:format|isToday)\(/)[0];
        const dataToDate = basePath === '$'
            ? (Array.isArray(jsonData) ? jsonData : [jsonData])
            : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
        return handleDateOperations(
            Array.isArray(dataToDate) ? dataToDate : [dataToDate],
            jsonPath.slice(basePath.length)
        );
    }

    if (jsonPath.match(/\.(?:sort|distinct|reverse)\(|\[\d*:\d*\]/)) {
        // Split at .sort( / .distinct( / .reverse( OR at a slice [n:m].
        // The slice [ may follow $, ], or a word char — never a dot — so we
        // match it as a separate alternative without requiring a preceding dot.
        // The colon is MANDATORY so single-index access like [0] falls through
        // to plain JSONPath (which handles it natively).
        const basePath = jsonPath.split(/\.(?:sort|distinct|reverse)\(|\[\d*:\d*\]/)[0];
        let dataToOp = basePath === '$'
            ? (Array.isArray(jsonData) ? jsonData : [jsonData])
            : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
        if (!Array.isArray(dataToOp)) dataToOp = dataToOp ? [dataToOp] : [];
        // For slices, the expression passed to handleArrayOperations must
        // include the [n:m] slice bracket; for method ops it includes .sort(...) etc.
        const remainder = jsonPath.slice(basePath.length);
        return handleArrayOperations(dataToOp, remainder);
    }

    if (jsonPath.match(/\.(toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)\(/)) {
        const basePath = jsonPath.split(/\.(?:toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)\(/)[0];
        const val = JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
        return handleStringOperations(val, jsonPath.slice(basePath.length));
    }

    return JSONPathPlus({ path: jsonPath, json: jsonData, wrap: true }) as JsonValue;
}

server.registerTool(
    "query",
    {
        description: "Query a local JSON file using standard JSONPath with custom extensions for sorting, aggregation, math, and string/date manipulation. Supports JSON5 (comments, trailing commas, single quotes) and caching for performance.",
        inputSchema: QueryArgumentsSchema,
    },
    async ({ path: filePath, jsonPath }) => {
        const jsonData = await readJsonFile(filePath);
        const result = resolveQuery(jsonData, jsonPath);
        return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
    }
);

server.registerTool(
    "filter",
    {
        description: "Extract and filter elements from an array in a local JSON file using advanced comparison and string matching logic. Supports JSON5 input files.",
        inputSchema: FilterArgumentsSchema,
    },
    async ({ path: filePath, jsonPath, condition }) => {
        const jsonData = await readJsonFile(filePath);
        let baseData = JSONPathPlus({ path: jsonPath, json: jsonData, wrap: false }) as JsonValue;
        if (!Array.isArray(baseData)) baseData = baseData ? [baseData] : [];
        const result = handleComplexFilter(baseData, condition);
        return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
    }
);

export async function startServer() {
    const transport = new StdioServerTransport();
    await server.connect(transport);
    console.error("mcp-json-reader running on stdio");
    const rootPath = getRootPath();
    if (rootPath) {
        console.error(`Root directory: ${rootPath}`);
    } else {
        console.error(`Root directory: ${process.cwd()} (default)`);
    }
}
