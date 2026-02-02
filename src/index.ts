#!/usr/bin/env node

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
    CallToolRequestSchema,
    ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";
import { readFile } from "fs/promises";
import JSONPath from 'jsonpath';

/**
 * mcp-json-reader: An MCP server to read and query local JSON files with extended syntax.
 */

// --- Helper Functions (Exported for Testing) ---

export async function readJsonFile(filePath: string) {
    try {
        const content = await readFile(filePath, "utf-8");
        return JSON.parse(content);
    } catch (error: any) {
        throw new Error(`Failed to read or parse JSON file at ${filePath}: ${error.message}`);
    }
}

export function handleArrayOperations(data: any[], expression: string): any[] {
    let result = [...data];

    // Handle sorting: .sort(field) or .sort(-field)
    const sortMatch = expression.match(/\.sort\(([-]?\w+)\)/);
    if (sortMatch) {
        const field = sortMatch[1];
        const isDesc = field.startsWith('-');
        const sortField = isDesc ? field.slice(1) : field;

        result.sort((a, b) => {
            const aVal = a[sortField];
            const bVal = b[sortField];
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
            .map(i => (typeof i === 'string' && (i.startsWith('{') || i.startsWith('['))) ? JSON.parse(i) : i);
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

export function handleAggregation(data: any[], operation: string): number {
    if (!Array.isArray(data) || data.length === 0) return 0;

    const getVal = (item: any, field: string) => {
        const val = item[field];
        return typeof val === 'number' ? val : (Number(val) || 0);
    };

    const sumMatch = operation.match(/\.sum\((\w+)\)/);
    if (sumMatch) return data.reduce((sum, item) => sum + getVal(item, sumMatch[1]), 0);

    const avgMatch = operation.match(/\.avg\((\w+)\)/);
    if (avgMatch) return data.reduce((sum, item) => sum + getVal(item, avgMatch[1]), 0) / data.length;

    const minMatch = operation.match(/\.min\((\w+)\)/);
    if (minMatch) return Math.min(...data.map(item => getVal(item, minMatch[1])));

    const maxMatch = operation.match(/\.max\((\w+)\)/);
    if (maxMatch) return Math.max(...data.map(item => getVal(item, maxMatch[1])));

    return 0;
}

export function handleNumericOperations(data: any[], expression: string): any {
    const values = data.map(v => typeof v === 'number' ? v : (Number(v) || 0));

    // Basic math: .math(+10), .math(*2)
    const mathMatch = expression.match(/\.math\(([\+\-\*\/\d\s\.]+)\)/);
    if (mathMatch) {
        const expr = mathMatch[1].trim();
        return values.map(num => {
            try {
                // Restricted evaluation
                const allowed = /^[0-9\+\-\*\/\s\.\(\)]+$/;
                if (!allowed.test(expr)) return 0;
                return new Function(`return ${num} ${expr}`)();
            } catch { return 0; }
        });
    }

    if (expression.includes('.round()')) return values.map(v => Math.round(v));
    if (expression.includes('.floor()')) return values.map(v => Math.floor(v));
    if (expression.includes('.ceil()')) return values.map(v => Math.ceil(v));
    if (expression.includes('.abs()')) return values.map(v => Math.abs(v));
    if (expression.includes('.sqrt()')) return values.map(v => Math.sqrt(v));
    if (expression.includes('.pow2()')) return values.map(v => Math.pow(v, 2));

    return values;
}

export function handleStringOperations(value: any, operation: string): any {
    if (typeof value !== 'string') return value;

    if (operation === '.toLowerCase()') return value.toLowerCase();
    if (operation === '.toUpperCase()') return value.toUpperCase();

    const startsWithMatch = operation.match(/\.startsWith\(['"](.+)['"]\)/);
    if (startsWithMatch) return value.startsWith(startsWithMatch[1]);

    const endsWithMatch = operation.match(/\.endsWith\(['"](.+)['"]\)/);
    if (endsWithMatch) return value.endsWith(endsWithMatch[1]);

    const containsMatch = operation.match(/\.contains\(['"](.+)['"]\)/);
    if (containsMatch) return value.includes(containsMatch[1]);

    const matchesMatch = operation.match(/\.matches\(['"](.+)['"]\)/);
    if (matchesMatch) return new RegExp(matchesMatch[1]).test(value);

    return value;
}

export function handleDateOperations(data: any[], expression: string): any[] {
    const formatMatch = expression.match(/\.format\(['"](.+)['"]\)/);
    if (formatMatch) {
        const format = formatMatch[1];
        return data.map(date => {
            const d = new Date(date);
            if (isNaN(d.getTime())) return date;
            return format
                .replace('YYYY', d.getFullYear().toString())
                .replace('MM', (d.getMonth() + 1).toString().padStart(2, '0'))
                .replace('DD', d.getDate().toString().padStart(2, '0'))
                .replace('HH', d.getHours().toString().padStart(2, '0'))
                .replace('mm', d.getMinutes().toString().padStart(2, '0'))
                .replace('ss', d.getSeconds().toString().padStart(2, '0'));
        });
    }

    if (expression === '.isToday()') {
        const today = new Date().toDateString();
        return data.map(date => new Date(date).toDateString() === today);
    }

    return data;
}

export function handleComplexFilter(data: any[], condition: string): any[] {
    return data.filter(item => {
        try {
            // String operations in filter
            const ops = ['.contains', '.startsWith', '.endsWith', '.matches'];
            for (const op of ops) {
                if (condition.includes(op)) {
                    const pattern = new RegExp(`@\\.(\\w+)\\${op}\\(['"](.+?)['"]\\)`);
                    const match = condition.match(pattern);
                    if (match) {
                        const [_, field, arg] = match;
                        const val = String(item[field] || '');
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
                const [_, field, op, rawValue] = compMatch;
                const itemValue = item[field];
                const compareValue = (rawValue.startsWith('"') || rawValue.startsWith("'"))
                    ? rawValue.slice(1, -1)
                    : Number(rawValue);

                switch (op) {
                    case '>': return itemValue > compareValue;
                    case '>=': return itemValue >= compareValue;
                    case '<': return itemValue < compareValue;
                    case '<=': return itemValue <= compareValue;
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
        version: "1.1.0"
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
                description: "Query a local JSON file with extended JSONPath (sort, sum, math, etc.)",
                inputSchema: {
                    type: "object",
                    properties: {
                        path: { type: "string" },
                        jsonPath: { type: "string" }
                    },
                    required: ["path", "jsonPath"],
                },
            },
            {
                name: "filter",
                description: "Filter an array in a local JSON file",
                inputSchema: {
                    type: "object",
                    properties: {
                        path: { type: "string" },
                        jsonPath: { type: "string" },
                        condition: { type: "string" }
                    },
                    required: ["path", "jsonPath", "condition"],
                },
            }
        ],
    };
});

server.setRequestHandler(CallToolRequestSchema, async (request) => {
    const { name, arguments: args } = request.params;

    try {
        if (name === "query") {
            const { path: filePath, jsonPath } = QueryArgumentsSchema.parse(args);
            const jsonData = await readJsonFile(filePath);
            let result: any;

            if (jsonPath === "$.length()") {
                result = Array.isArray(jsonData) ? jsonData.length : (jsonData && typeof jsonData === 'object' ? Object.keys(jsonData).length : 0);
            } else if (jsonPath.match(/\.(sum|avg|min|max)\(/)) {
                const basePath = jsonPath.split(/\.(?:sum|avg|min|max)/)[0];
                const dataToAgg = basePath === '$' ? (Array.isArray(jsonData) ? jsonData : [jsonData]) : JSONPath.value(jsonData, basePath);
                result = handleAggregation(Array.isArray(dataToAgg) ? dataToAgg : [], jsonPath.slice(basePath.length));
            } else if (jsonPath.match(/\.(math|round|floor|ceil|abs|sqrt|pow2)/)) {
                const basePath = jsonPath.split(/\.(?:math|round|floor|ceil|abs|sqrt|pow2)/)[0];
                const dataToMath = basePath === '$' ? (Array.isArray(jsonData) ? jsonData : [jsonData]) : JSONPath.value(jsonData, basePath);
                result = handleNumericOperations(Array.isArray(dataToMath) ? dataToMath : [dataToMath], jsonPath.slice(basePath.length));
            } else if (jsonPath.match(/\.(format|isToday)\(/)) {
                const basePath = jsonPath.split(/\.(?:format|isToday)/)[0];
                const dataToDate = basePath === '$' ? (Array.isArray(jsonData) ? jsonData : [jsonData]) : JSONPath.value(jsonData, basePath);
                result = handleDateOperations(Array.isArray(dataToDate) ? dataToDate : [dataToDate], jsonPath.slice(basePath.length));
            } else if (jsonPath.match(/\.(sort|distinct|reverse|\[\d*:?\d*\])/)) {
                const basePath = jsonPath.split(/\.(?:sort|distinct|reverse|\[)/)[0];
                let dataToOp = basePath === '$' ? (Array.isArray(jsonData) ? jsonData : [jsonData]) : JSONPath.value(jsonData, basePath);
                if (!Array.isArray(dataToOp)) dataToOp = dataToOp ? [dataToOp] : [];
                result = handleArrayOperations(dataToOp, jsonPath.slice(basePath.length || 0));
            } else if (jsonPath.match(/\.(toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)\(/)) {
                const basePath = jsonPath.split(/\.(?:toLowerCase|toUpperCase|startsWith|endsWith|contains|matches)/)[0];
                const val = JSONPath.value(jsonData, basePath);
                result = handleStringOperations(val, jsonPath.slice(basePath.length || 0));
            } else {
                result = JSONPath.query(jsonData, jsonPath);
            }

            return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
        }

        if (name === "filter") {
            const { path: filePath, jsonPath, condition } = FilterArgumentsSchema.parse(args);
            const jsonData = await readJsonFile(filePath);
            let baseData = JSONPath.value(jsonData, jsonPath);
            if (!Array.isArray(baseData)) baseData = baseData ? [baseData] : [];
            const result = handleComplexFilter(baseData, condition);
            return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
        }

        throw new Error(`Unknown tool: ${name}`);
    } catch (error: any) {
        return { content: [{ type: "text", text: `Error: ${error.message}` }], isError: true };
    }
});

async function main() {
    const transport = new StdioServerTransport();
    await server.connect(transport);
    console.error("mcp-json-reader running on stdio");
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});