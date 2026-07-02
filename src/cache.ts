import { stat, readFile } from "fs/promises";
import path from "path";
import JSON5 from "json5";
import { JsonValue, CacheEntry } from "./types.js";

/**
 * Maximum raw file size in bytes that readJsonFile will accept.
 * 1.5 GB — safe for a 16 GB system with V8's default ~4 GB heap.
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

export const MAX_CACHE_ENTRIES = 10;
export const jsonCache = new Map<string, CacheEntry>();

export function clearCache(): void {
    jsonCache.clear();
}

export function getCacheStats(): { entries: number } {
    return { entries: jsonCache.size };
}

const args = process.argv.slice(2);
let rootPath = process.env.MCP_JSON_ROOT || "";

for (let i = 0; i < args.length; i++) {
    if (args[i] === "--root" && args[i + 1]) {
        rootPath = path.resolve(args[i + 1]);
        i++;
    }
}

export function getRootPath(): string {
    return rootPath;
}

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
            jsonCache.delete(fullPath);
            jsonCache.set(fullPath, cached);
            return cached.data;
        }

        const content = await readFile(fullPath, "utf-8");
        // JSON5 is a superset of JSON: it accepts strict JSON plus comments,
        // trailing commas, single-quoted strings, hex numbers, Infinity/NaN,
        // unquoted keys, and multi-line strings. This fulfills the README's
        // promise of "JSON with comments" support while remaining a drop-in
        // replacement for strict JSON files.
        const data: JsonValue = JSON5.parse(content);

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
