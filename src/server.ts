import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
    CallToolRequestSchema,
    ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
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

export const server = new Server(
    {
        name: "mcp-json-reader",
        version: "1.1.3"
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
                    : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
                result = handleAggregation(
                    Array.isArray(dataToAgg) ? dataToAgg : [],
                    jsonPath.slice(basePath.length)
                );
            } else if (jsonPath.match(/\.(math|round|floor|ceil|abs|sqrt|pow2)/)) {
                const basePath = jsonPath.split(/\.(?:math|round|floor|ceil|abs|sqrt|pow2)/)[0];
                const dataToMath = basePath === '$'
                    ? (Array.isArray(jsonData) ? jsonData : [jsonData])
                    : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
                result = handleNumericOperations(
                    Array.isArray(dataToMath) ? dataToMath : [dataToMath],
                    jsonPath.slice(basePath.length)
                );
            } else if (jsonPath.match(/\.(format|isToday)\(/)) {
                const basePath = jsonPath.split(/\.(?:format|isToday)/)[0];
                const dataToDate = basePath === '$'
                    ? (Array.isArray(jsonData) ? jsonData : [jsonData])
                    : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
                result = handleDateOperations(
                    Array.isArray(dataToDate) ? dataToDate : [dataToDate],
                    jsonPath.slice(basePath.length)
                );
            } else if (jsonPath.match(/\.(sort|distinct|reverse|\[\d*:?\d*\])/)) {
                const basePath = jsonPath.split(/\.(?:sort|distinct|reverse|\[)/)[0];
                let dataToOp = basePath === '$'
                    ? (Array.isArray(jsonData) ? jsonData : [jsonData])
                    : JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
                if (!Array.isArray(dataToOp)) dataToOp = dataToOp ? [dataToOp] : [];
                result = handleArrayOperations(dataToOp, jsonPath.slice(basePath.length || 0));
            } else if (jsonPath.match(/\.(toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)\(/)) {
                const basePath = jsonPath.split(/\.(?:toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)/)[0];
                const val = JSONPathPlus({ path: basePath, json: jsonData, wrap: false }) as JsonValue;
                result = handleStringOperations(val, jsonPath.slice(basePath.length || 0));
            } else {
                result = JSONPathPlus({ path: jsonPath, json: jsonData, wrap: true }) as JsonValue;
            }

            return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
        }

        if (name === "filter") {
            const { path: filePath, jsonPath, condition } = FilterArgumentsSchema.parse(toolArgs);
            const jsonData = await readJsonFile(filePath);
            let baseData = JSONPathPlus({ path: jsonPath, json: jsonData, wrap: false }) as JsonValue;
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
