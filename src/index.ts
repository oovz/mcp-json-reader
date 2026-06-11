#!/usr/bin/env node

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
    CallToolRequestSchema,
    ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";
import { readFile, stat } from "fs/promises";
import JSONPath from 'jsonpath';
import path from "path";

/**
 * mcp-json-reader: An MCP server to read and query local JSON files with extended syntax.
 *
 * ## V8 Heap Memory Limitation
 *
 * On a 64-bit system, V8's default max heap is approximately 4 GB (tunable via
 * --max-old-space-size). JSON.parse() builds a full in-memory object graph that
 * typically consumes 2–6× the raw file size in heap. For a 16 GB system:
 *
 *   - OS + other processes need ~2–4 GB
 *   - V8 heap budget: ~4 GB (default)
 *   - Safe raw JSON file limit: ~1.5 GB (to leave room for the parsed graph,
 *     the cache, query results, and headroom for GC pressure)
 *
 * This server enforces MAX_FILE_SIZE_BYTES = 1.5 GB. Files exceeding this are
 * rejected before reading to prevent V8 OOM crashes. The in-memory cache stores
 * up to MAX_CACHE_ENTRIES parsed objects with LRU eviction. Each cached entry
 * holds a reference to the full parsed object graph — on a 16 GB system with
 * 10 cached files, worst case memory is ~10 × (file_size × expansion_factor).
 *
 * To raise the limit, start Node with: node --max-old-space-size=8192
 * and set the MCP_MAX_FILE_SIZE_MB environment variable.
 */

// --- JSON value types (replaces all `any` usage) ---

type JsonPrimitive = string | number | boolean | null;
type JsonValue = JsonPrimitive | JsonObject | JsonArray;
interface JsonObject { [key: string]: JsonValue }
type JsonArray = JsonValue[];

// --- Configuration and Caching ---

const args = process.argv.slice(2);
let rootPath = process.env.MCP_JSON_ROOT || "";

for (let i = 0; i < args.length; i++) {
    if (args[i] === "--root" && args[i + 1]) {
        rootPath = path.resolve(args[i + 1]);
        i++;
    }
}

/**
 * Maximum raw file size in bytes that readJsonFile will accept.
 * 1.5 GB — safe for a 16 GB system with V8's default ~4 GB heap.
 * JSON.parse inflates by 2–6×, so a 1.5 GB file may consume 3–9 GB of heap.
 * Override via MCP_MAX_FILE_SIZE_MB env var.
 */
export const MAX_FILE_SIZE_BYTES: number = (() => {
    const envMb = process.env.MCP_MAX_FILE_SIZE_MB;
    if (envMb) {
        const mb = Number(envMb);
        if (Number.isFinite(mb) && mb > 0) {
            return mb * 1024 * 1024;
        }
    }
    return 1.5 * 1024 * 1024 * 1024; // 1.5 GB
})();

const MAX_CACHE_ENTRIES = 10;

interface CacheEntry {
    data: JsonValue;
    mtime: number;
}

const jsonCache = new Map<string, CacheEntry>();

/** Clear the file cache. Exported for testing. */
export function clearCache(): void {
    jsonCache.clear();
}

/** Return cache statistics. Exported for testing. */
export function getCacheStats(): { entries: number } {
    return { entries: jsonCache.size };
}

// --- Helper Functions (Exported for Testing) ---

export function resolvePath(filePath: string): string {
    if (path.isAbsolute(filePath)) {
        return filePath;
    }
    if (rootPath) {
        return path.resolve(rootPath, filePath);
    }
    return path.resolve(process.cwd(), filePath);
}

export async function readJsonFile(filePath: string): Promise<JsonValue> {
    const fullPath = resolvePath(filePath);
    try {
        const stats = await stat(fullPath);
        const mtime = stats.mtimeMs;

        // File size guard — prevent V8 OOM on large files
        if (stats.size > MAX_FILE_SIZE_BYTES) {
            const sizeMb = (stats.size / (1024 * 1024)).toFixed(1);
            const limitMb = (MAX_FILE_SIZE_BYTES / (1024 * 1024)).toFixed(1);
            throw new Error(
                `File size ${sizeMb} MB exceeds limit of ${limitMb} MB. ` +
                `V8 heap cannot safely parse files this large. ` +
                `Set MCP_MAX_FILE_SIZE_MB or use --max-old-space-size to increase limits.`
            );
        }

        const cached = jsonCache.get(fullPath);
        if (cached && cached.mtime === mtime) {
            // LRU: move to end (most recently used)
            jsonCache.delete(fullPath);
            jsonCache.set(fullPath, cached);
            return cached.data;
        }

        const content = await readFile(fullPath, "utf-8");
        const data: JsonValue = JSON.parse(content);

        // LRU eviction: remove oldest (first) entry when at capacity
        if (jsonCache.size >= MAX_CACHE_ENTRIES) {
            const firstKey = jsonCache.keys().next().value;
            if (firstKey !== undefined) jsonCache.delete(firstKey);
        }

        jsonCache.set(fullPath, { data, mtime });
        return data;
    } catch (error: unknown) {
        if (error instanceof Error) {
            throw new Error(`Failed to read or parse JSON file at ${fullPath}: ${error.message}`);
        }
        throw new Error(`Failed to read or parse JSON file at ${fullPath}: unknown error`);
    }
}

// Type guard helpers
function isRecord(value: JsonValue): value is JsonObject {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function toNumber(value: JsonValue): number {
    if (typeof value === 'number') return value;
    if (typeof value === 'string') return Number(value) || 0;
    return 0;
}

function getField(item: JsonValue, field: string): JsonValue {
    if (isRecord(item)) {
        return item[field] ?? null;
    }
    return null;
}

export function handleArrayOperations(data: JsonArray, expression: string): JsonArray {
    let result = [...data];

    // Handle sorting: .sort(field) or .sort(-field)
    const sortMatch = expression.match(/\.sort\(([-]?\w+)\)/);
    if (sortMatch) {
        const field = sortMatch[1];
        const isDesc = field.startsWith('-');
        const sortField = isDesc ? field.slice(1) : field;

        result.sort((a, b) => {
            const aVal = getField(a, sortField);
            const bVal = getField(b, sortField);
            if (aVal == null) return 1;
            if (bVal == null) return -1;
            if (aVal === bVal) return 0;
            return isDesc ?
                (bVal > aVal ? 1 : -1) :
                (aVal > bVal ? 1 : -1);
        });
    }

    // Handle distinct
    if (expression.includes('.distinct()')) {
        result = Array.from(new Set(result.map(i => (typeof i === 'object' && i !== null) ? JSON.stringify(i) : i)))
            .map(i => {
                if (typeof i === 'string' && (i.startsWith('{') || i.startsWith('['))) {
                    return JSON.parse(i) as JsonValue;
                }
                return i as JsonValue;
            });
    }

    // Handle reverse
    if (expression.includes(".reverse()")) {
        result.reverse();
    }

    // Handle slicing: [start:end]
    const sliceMatch = expression.match(/\[(\d*):(\d*)\]/);
    if (sliceMatch) {
        const start = sliceMatch[1] ? parseInt(sliceMatch[1]) : 0;
        const end = sliceMatch[2] ? parseInt(sliceMatch[2]) : undefined;
        result = result.slice(start, end);
    }

    return result;
}

export function handleAggregation(data: JsonArray, operation: string): number {
    if (!Array.isArray(data) || data.length === 0) return 0;

    const getVal = (item: JsonValue, field: string): number => {
        const val = getField(item, field);
        return toNumber(val);
    };

    const sumMatch = operation.match(/\.sum\((\w+)\)/);
    if (sumMatch) {
        const field = sumMatch[1];
        return data.reduce<number>((sum, item) => sum + getVal(item, field), 0);
    }

    const avgMatch = operation.match(/\.avg\((\w+)\)/);
    if (avgMatch) {
        const field = avgMatch[1];
        return data.reduce<number>((sum, item) => sum + getVal(item, field), 0) / data.length;
    }

    const minMatch = operation.match(/\.min\((\w+)\)/);
    if (minMatch) {
        const field = minMatch[1];
        return Math.min(...data.map(item => getVal(item, field)));
    }

    const maxMatch = operation.match(/\.max\((\w+)\)/);
    if (maxMatch) {
        const field = maxMatch[1];
        return Math.max(...data.map(item => getVal(item, field)));
    }

    return 0;
}

export function handleNumericOperations(data: JsonArray, expression: string): JsonArray {
    const values = data.map(v => toNumber(v));

    // Basic math: .math(+10), .math(*2)
    const mathMatch = expression.match(/\.math\(([\+\-\*\/\d\s\.]+)\)/);
    if (mathMatch) {
        const expr = mathMatch[1].trim();
        return values.map(num => {
            try {
                // Restricted evaluation — only digits and arithmetic operators
                const allowed = /^[0-9\+\-\*\/\s\.()]+$/;
                if (!allowed.test(expr)) return 0;
                return new Function(`return ${num} ${expr}`)() as number;
            } catch { return 0; }
        });
    }

    // If the expression looks like .math(...) but the regex didn't match
    // (non-numeric content), return zeros to prevent pass-through
    if (expression.includes('.math(')) {
        return values.map(() => 0);
    }

    if (expression.includes('.round()')) return values.map(v => Math.round(v));
    if (expression.includes('.floor()')) return values.map(v => Math.floor(v));
    if (expression.includes('.ceil()')) return values.map(v => Math.ceil(v));
    if (expression.includes('.abs()')) return values.map(v => Math.abs(v));
    if (expression.includes('.sqrt()')) return values.map(v => Math.sqrt(v));
    if (expression.includes('.pow2()')) return values.map(v => Math.pow(v, 2));

    return values;
}

export function handleStringOperations(value: JsonValue, operation: string): JsonValue {
    if (typeof value !== 'string') return value;

    if (operation === '.toLowerCase()') return value.toLowerCase();
    if (operation === '.toUpperCase()') return value.toUpperCase();

    const startsWithMatch = operation.match(/\.startsWith\(['"](.+)['"]\)/);
    if (startsWithMatch) return value.startsWith(startsWithMatch[1]);

    const endsWithMatch = operation.match(/\.endsWith\(['"](.+)['"]\)/);
    if (endsWithMatch) return value.endsWith(endsWithMatch[1]);

    // Use .* to allow empty string matches
    const containsMatch = operation.match(/\.contains\(['"](.*)['"]\)/);
    if (containsMatch) return value.includes(containsMatch[1]);

    const matchesMatch = operation.match(/\.matches\(['"](.+)['"]\)/);
    if (matchesMatch) return new RegExp(matchesMatch[1]).test(value);

    return value;
}

export function handleDateOperations(data: JsonArray, expression: string): JsonArray {
    const formatMatch = expression.match(/\.format\(['"](.+)['"]\)/);
    if (formatMatch) {
        const format = formatMatch[1];
        return data.map(date => {
            if (typeof date !== 'string' && typeof date !== 'number') return date;
            const d = new Date(date as string | number);
            if (isNaN(d.getTime())) return date;
            // Use UTC methods to avoid timezone-dependent results
            return format
                .replace('YYYY', d.getUTCFullYear().toString())
                .replace('MM', (d.getUTCMonth() + 1).toString().padStart(2, '0'))
                .replace('DD', d.getUTCDate().toString().padStart(2, '0'))
                .replace('HH', d.getUTCHours().toString().padStart(2, '0'))
                .replace('mm', d.getUTCMinutes().toString().padStart(2, '0'))
                .replace('ss', d.getUTCSeconds().toString().padStart(2, '0'));
        });
    }

    if (expression === '.isToday()') {
        const today = new Date().toDateString();
        return data.map(date => {
            if (typeof date !== 'string' && typeof date !== 'number') return false;
            return new Date(date as string | number).toDateString() === today;
        });
    }

    return data;
}

export function handleComplexFilter(data: JsonArray, condition: string): JsonArray {
    return data.filter(item => {
        try {
            if (!isRecord(item)) return false;

            // String operations in filter
            const ops = ['.contains', '.startsWith', '.endsWith', '.matches'] as const;
            for (const op of ops) {
                if (condition.includes(op)) {
                    const pattern = new RegExp(`@\\.(\\w+)\\${op}\\(['"](.+?)['"]\\)`);
                    const match = condition.match(pattern);
                    if (match) {
                        const [, field, arg] = match;
                        const val = String(item[field] ?? '');
                        if (op === '.contains') return val.includes(arg);
                        if (op === '.startsWith') return val.startsWith(arg);
                        if (op === '.endsWith') return val.endsWith(arg);
                        if (op === '.matches') return new RegExp(arg).test(val);
                    }
                }
            }

            // Comparison operations
            const compMatch = condition.match(/@\.(\w+)\s*([><=!]+)\s*(.+)/);
            if (compMatch) {
                const [, field, op, rawValue] = compMatch;
                const itemValue = item[field];
                const compareValue: JsonValue = (rawValue.startsWith('"') || rawValue.startsWith("'"))
                    ? rawValue.slice(1, -1)
                    : Number(rawValue);

                switch (op) {
                    case '>': return (itemValue as number) > (compareValue as number);
                    case '>=': return (itemValue as number) >= (compareValue as number);
                    case '<': return (itemValue as number) < (compareValue as number);
                    case '<=': return (itemValue as number) <= (compareValue as number);
                    case '==': return itemValue == compareValue;
                    case '!=': return itemValue != compareValue;
                }
            }
            return false;
        } catch {
            return false;
        }
    });
}

// --- Server Implementation ---

const server = new Server(
    {
        name: "mcp-json-reader",
        version: "1.1.2"
    },
    {
        capabilities: {
            tools: {}
        }
    }
);

const QueryArgumentsSchema = z.object({
    path: z.string().describe("Local path to the JSON file"),
    jsonPath: z.string().describe("JSONPath with extended syntax"),
});

const FilterArgumentsSchema = z.object({
    path: z.string().describe("Local path to the JSON file"),
    jsonPath: z.string().describe("JSONPath to the array"),
    condition: z.string().describe("Condition like '@.price > 10'"),
});

server.setRequestHandler(ListToolsRequestSchema, async () => {
    return {
        tools: [
            {
                name: "query",
                description: "Query a local JSON file using standard JSONPath with custom extensions for sorting, aggregation, math, and string/date manipulation. Supports caching for performance.",
                inputSchema: {
                    type: "object" as const,
                    properties: {
                        path: {
                            type: "string",
                            description: "Absolute path or path relative to the configured root directory"
                        },
                        jsonPath: {
                            type: "string",
                            description: "JSONPath expression (e.g. '$.store.book[*].author'). Can include extensions like '.sort(field)', '.sum(field)', '.math(*2)', etc."
                        }
                    },
                    required: ["path", "jsonPath"],
                },
            },
            {
                name: "filter",
                description: "Extract and filter elements from an array in a local JSON file using advanced comparison and string matching logic.",
                inputSchema: {
                    type: "object" as const,
                    properties: {
                        path: {
                            type: "string",
                            description: "Absolute path or path relative to the configured root directory"
                        },
                        jsonPath: {
                            type: "string",
                            description: "JSONPath to the target array (e.g. '$.store.book')"
                        },
                        condition: {
                            type: "string",
                            description: "Filter condition. Examples: '@.price > 10', '@.category == \"fiction\"', '@.title.contains(\"Lord\")'"
                        }
                    },
                    required: ["path", "jsonPath", "condition"],
                },
            }
        ],
    };
});

server.setRequestHandler(CallToolRequestSchema, async (request) => {
    const { name, arguments: toolArgs } = request.params;

    try {
        if (name === "query") {
            const { path: filePath, jsonPath } = QueryArgumentsSchema.parse(toolArgs);
            const jsonData = await readJsonFile(filePath);
            let result: JsonValue;

            if (jsonPath === "$.length()") {
                result = Array.isArray(jsonData)
                    ? jsonData.length
                    : (isRecord(jsonData) ? Object.keys(jsonData).length : 0);
            } else if (jsonPath.match(/\.(sum|avg|min|max)\(/)) {
                const basePath = jsonPath.split(/\.(?:sum|avg|min|max)/)[0];
                const dataToAgg = basePath === '$'
                    ? (Array.isArray(jsonData) ? jsonData : [jsonData])
                    : JSONPath.value(jsonData, basePath) as JsonValue;
                result = handleAggregation(
                    Array.isArray(dataToAgg) ? dataToAgg : [],
                    jsonPath.slice(basePath.length)
                );
            } else if (jsonPath.match(/\.(math|round|floor|ceil|abs|sqrt|pow2)/)) {
                const basePath = jsonPath.split(/\.(?:math|round|floor|ceil|abs|sqrt|pow2)/)[0];
                const dataToMath = basePath === '$'
                    ? (Array.isArray(jsonData) ? jsonData : [jsonData])
                    : JSONPath.value(jsonData, basePath) as JsonValue;
                result = handleNumericOperations(
                    Array.isArray(dataToMath) ? dataToMath : [dataToMath],
                    jsonPath.slice(basePath.length)
                );
            } else if (jsonPath.match(/\.(format|isToday)\(/)) {
                const basePath = jsonPath.split(/\.(?:format|isToday)/)[0];
                const dataToDate = basePath === '$'
                    ? (Array.isArray(jsonData) ? jsonData : [jsonData])
                    : JSONPath.value(jsonData, basePath) as JsonValue;
                result = handleDateOperations(
                    Array.isArray(dataToDate) ? dataToDate : [dataToDate],
                    jsonPath.slice(basePath.length)
                );
            } else if (jsonPath.match(/\.(sort|distinct|reverse|\[\d*:?\d*\])/)) {
                const basePath = jsonPath.split(/\.(?:sort|distinct|reverse|\[)/)[0];
                let dataToOp = basePath === '$'
                    ? (Array.isArray(jsonData) ? jsonData : [jsonData])
                    : JSONPath.value(jsonData, basePath) as JsonValue;
                if (!Array.isArray(dataToOp)) dataToOp = dataToOp ? [dataToOp] : [];
                result = handleArrayOperations(dataToOp, jsonPath.slice(basePath.length || 0));
            } else if (jsonPath.match(/\.(toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)\(/)) {
                const basePath = jsonPath.split(/\.(?:toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)/)[0];
                const val = JSONPath.value(jsonData, basePath) as JsonValue;
                result = handleStringOperations(val, jsonPath.slice(basePath.length || 0));
            } else {
                result = JSONPath.query(jsonData, jsonPath) as JsonValue;
            }

            return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
        }

        if (name === "filter") {
            const { path: filePath, jsonPath, condition } = FilterArgumentsSchema.parse(toolArgs);
            const jsonData = await readJsonFile(filePath);
            let baseData = JSONPath.value(jsonData, jsonPath) as JsonValue;
            if (!Array.isArray(baseData)) baseData = baseData ? [baseData] : [];
            const result = handleComplexFilter(baseData, condition);
            return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
        }

        throw new Error(`Unknown tool: ${name}`);
    } catch (error: unknown) {
        const message = error instanceof Error ? error.message : String(error);
        return { content: [{ type: "text", text: `Error: ${message}` }], isError: true };
    }
});

async function main() {
    const transport = new StdioServerTransport();
    await server.connect(transport);
    console.error("mcp-json-reader running on stdio");
    if (rootPath) {
        console.error(`Root directory: ${rootPath}`);
    } else {
        console.error(`Root directory: ${process.cwd()} (default)`);
    }
}

main().catch((err: unknown) => {
    console.error(err);
    process.exit(1);
});
