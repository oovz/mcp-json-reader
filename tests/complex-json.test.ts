import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
    readJsonFile,
    clearCache,
    handleArrayOperations,
    handleAggregation,
    handleComplexFilter,
    resolveQuery,
} from '../src/index.js';
import path from 'path';
import { fileURLToPath } from 'url';
import { writeFile, unlink } from 'fs/promises';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// ============================================================
// SECTION: JSON5 / JSON-with-comments parsing
// Verifies the README's promise that the server parses JSON
// with comments and other relaxed-JSON syntax (JSON5 spec).
// Ref: https://spec.json5.org/
// ============================================================
describe('JSON5 / JSON-with-comments parsing', () => {
    beforeEach(() => clearCache());

    it('parses single-line // comments', async () => {
        const tmp = path.join(__dirname, '_json5_line.tmp');
        try {
            await writeFile(tmp, `{
  // this is a comment
  "key": "value"
}`, 'utf-8');
            const data = await readJsonFile(tmp) as { key: string };
            expect(data.key).toBe('value');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses block /* */ comments (multi-line)', async () => {
        const tmp = path.join(__dirname, '_json5_block.tmp');
        try {
            await writeFile(tmp, `{
  /* multi
     line
     block */
  "key": "value"
}`, 'utf-8');
            const data = await readJsonFile(tmp) as { key: string };
            expect(data.key).toBe('value');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses inline block comments', async () => {
        const tmp = path.join(__dirname, '_json5_inline.tmp');
        try {
            await writeFile(tmp, `{"a": /* x */ 1}`, 'utf-8');
            const data = await readJsonFile(tmp) as { a: number };
            expect(data.a).toBe(1);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('does NOT strip comment-like sequences inside strings', async () => {
        const tmp = path.join(__dirname, '_json5_str.tmp');
        try {
            await writeFile(tmp, `{
  "a": "this is // not a comment",
  "b": "this is /* not a comment */"
}`, 'utf-8');
            const data = await readJsonFile(tmp) as { a: string; b: string };
            expect(data.a).toBe('this is // not a comment');
            expect(data.b).toBe('this is /* not a comment */');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses trailing commas in arrays and objects', async () => {
        const tmp = path.join(__dirname, '_json5_trailing.tmp');
        try {
            await writeFile(tmp, `{
  "arr": [1, 2, 3,],
  "obj": { "a": 1, "b": 2, }
}`, 'utf-8');
            const data = await readJsonFile(tmp) as { arr: number[]; obj: { a: number; b: number } };
            expect(data.arr).toEqual([1, 2, 3]);
            expect(data.obj).toEqual({ a: 1, b: 2 });
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses single-quoted strings', async () => {
        const tmp = path.join(__dirname, '_json5_single.tmp');
        try {
            await writeFile(tmp, `{'key': 'value'}`, 'utf-8');
            const data = await readJsonFile(tmp) as { key: string };
            expect(data.key).toBe('value');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses unquoted keys (valid ES5 identifiers)', async () => {
        const tmp = path.join(__dirname, '_json5_unquoted.tmp');
        try {
            await writeFile(tmp, `{name: "Alice", _id: 1, $key: "x"}`, 'utf-8');
            const data = await readJsonFile(tmp) as { name: string; _id: number; $key: string };
            expect(data.name).toBe('Alice');
            expect(data._id).toBe(1);
            expect(data.$key).toBe('x');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses hexadecimal numbers', async () => {
        const tmp = path.join(__dirname, '_json5_hex.tmp');
        try {
            await writeFile(tmp, `{"hex": 0xFF}`, 'utf-8');
            const data = await readJsonFile(tmp) as { hex: number };
            expect(data.hex).toBe(255);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses leading and trailing decimal points', async () => {
        const tmp = path.join(__dirname, '_json5_decimals.tmp');
        try {
            await writeFile(tmp, `{"leading": .5, "trailing": 5.}`, 'utf-8');
            const data = await readJsonFile(tmp) as { leading: number; trailing: number };
            expect(data.leading).toBe(0.5);
            expect(data.trailing).toBe(5);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses explicit plus sign', async () => {
        const tmp = path.join(__dirname, '_json5_plus.tmp');
        try {
            await writeFile(tmp, `{"positive": +42}`, 'utf-8');
            const data = await readJsonFile(tmp) as { positive: number };
            expect(data.positive).toBe(42);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses Infinity, -Infinity, NaN', async () => {
        const tmp = path.join(__dirname, '_json5_special.tmp');
        try {
            await writeFile(tmp, `{"inf": Infinity, "negInf": -Infinity, "nan": NaN}`, 'utf-8');
            const data = await readJsonFile(tmp) as { inf: number; negInf: number; nan: number };
            expect(data.inf).toBe(Infinity);
            expect(data.negInf).toBe(-Infinity);
            expect(Number.isNaN(data.nan)).toBe(true);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses multi-line strings with backslash continuation', async () => {
        const tmp = path.join(__dirname, '_json5_multiline.tmp');
        try {
            await writeFile(tmp, `{"ml": "line1 \\
line2"}`, 'utf-8');
            const data = await readJsonFile(tmp) as { ml: string };
            expect(data.ml).toBe('line1 line2');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses the comprehensive JSON5 fixture file', async () => {
        const fixture = path.join(__dirname, 'mock-json5.json5');
        const data = await readJsonFile(fixture) as {
            unquoted: string;
            singleQuotes: string;
            nestedQuotes: string;
            commentInString: string;
            blockInString: string;
            trailingArray: number[];
            hex: number;
            leading: number;
            trailing: number;
            positive: number;
            scientific: number;
            scientificNeg: number;
            infinity: number;
            negativeInfinity: number;
            nan: number;
            unicode: string;
            emoji: string;
            largeNumber: number;
            duplicate: string;
            nested: { level1: { level2: { level3: { level4: { level5: { data: number[] } } } } } };
        };
        expect(data.unquoted).toBe('value');
        expect(data.singleQuotes).toBe('single');
        expect(data.nestedQuotes).toBe('contains "double" quotes');
        expect(data.commentInString).toBe('this is // not a comment');
        expect(data.blockInString).toBe('this is /* not a comment */');
        expect(data.trailingArray).toEqual([1, 2, 3]);
        expect(data.hex).toBe(255);
        expect(data.leading).toBe(0.5);
        expect(data.trailing).toBe(5);
        expect(data.positive).toBe(42);
        expect(data.scientific).toBe(1000);
        expect(data.scientificNeg).toBeCloseTo(1.5e-10, 15);
        expect(data.infinity).toBe(Infinity);
        expect(data.negativeInfinity).toBe(-Infinity);
        expect(Number.isNaN(data.nan)).toBe(true);
        expect(data.unicode).toBe('Hello');
        expect(data.emoji).toBe('\uD83D\uDE00');
        expect(data.largeNumber).toBe(9007199254740992); // precision loss
        expect(data.duplicate).toBe('second'); // last wins
        expect(data.nested.level1.level2.level3.level4.level5.data).toEqual([1, 2, 3, 4, 5]);
    });
});

// ============================================================
// SECTION: Strict JSON edge cases (RFC 8259 / RFC 7493)
// Ref: https://datatracker.ietf.org/doc/html/rfc8259
// Ref: https://www.rfc-editor.org/rfc/rfc7493
// ============================================================
describe('Strict JSON edge cases', () => {
    beforeEach(() => clearCache());

    it('parses scientific notation (valid strict JSON)', async () => {
        const tmp = path.join(__dirname, '_strict_sci.tmp');
        try {
            await writeFile(tmp, `{"a": 1e3, "b": 1.5E-10, "c": 2E+5}`, 'utf-8');
            const data = await readJsonFile(tmp) as { a: number; b: number; c: number };
            expect(data.a).toBe(1000);
            expect(data.b).toBeCloseTo(1.5e-10, 15);
            expect(data.c).toBe(200000);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses unicode escapes and surrogate pairs', async () => {
        const tmp = path.join(__dirname, '_strict_unicode.tmp');
        try {
            await writeFile(tmp, `{"u": "\\u0048\\u0065\\u006C\\u006C\\u006F", "e": "\\uD83D\\uDE00"}`, 'utf-8');
            const data = await readJsonFile(tmp) as { u: string; e: string };
            expect(data.u).toBe('Hello');
            expect(data.e).toBe('\uD83D\uDE00');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses UTF-8 BOM at start of file', async () => {
        const tmp = path.join(__dirname, '_strict_bom.tmp');
        try {
            // Write BOM (EF BB BF) followed by JSON
            const bom = Buffer.from([0xEF, 0xBB, 0xBF]);
            const content = Buffer.concat([bom, Buffer.from('{"key":"value"}', 'utf-8')]);
            await writeFile(tmp, content);
            const data = await readJsonFile(tmp) as { key: string };
            expect(data.key).toBe('value');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses deeply nested structure (50 levels)', async () => {
        const tmp = path.join(__dirname, '_strict_deep.tmp');
        try {
            let s = '{"value": 42}';
            for (let i = 0; i < 50; i++) s = `{"n":${s}}`;
            await writeFile(tmp, s, 'utf-8');
            const data = await readJsonFile(tmp) as any;
            let cur: any = data;
            for (let i = 0; i < 50; i++) cur = cur.n;
            expect(cur.value).toBe(42);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('handles duplicate keys (last value wins per JSON spec)', async () => {
        const tmp = path.join(__dirname, '_strict_dup.tmp');
        try {
            await writeFile(tmp, `{"k": "first", "k": "second"}`, 'utf-8');
            const data = await readJsonFile(tmp) as { k: string };
            expect(data.k).toBe('second');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses large number exceeding MAX_SAFE_INTEGER (precision loss)', async () => {
        const tmp = path.join(__dirname, '_strict_bignum.tmp');
        try {
            await writeFile(tmp, `{"big": 9007199254740993}`, 'utf-8');
            const data = await readJsonFile(tmp) as { big: number };
            // 2^53 + 1 cannot be represented as a double — rounds to 2^53
            expect(data.big).toBe(9007199254740992);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses empty array and empty object', async () => {
        const tmp = path.join(__dirname, '_strict_empty.tmp');
        try {
            await writeFile(tmp, `{"arr": [], "obj": {}}`, 'utf-8');
            const data = await readJsonFile(tmp) as { arr: unknown[]; obj: object };
            expect(data.arr).toEqual([]);
            expect(data.obj).toEqual({});
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses top-level array', async () => {
        const tmp = path.join(__dirname, '_strict_toparr.tmp');
        try {
            await writeFile(tmp, `[1, 2, 3]`, 'utf-8');
            const data = await readJsonFile(tmp) as unknown[];
            expect(data).toEqual([1, 2, 3]);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses top-level primitive', async () => {
        const tmp = path.join(__dirname, '_strict_primitive.tmp');
        try {
            await writeFile(tmp, `42`, 'utf-8');
            const data = await readJsonFile(tmp) as number;
            expect(data).toBe(42);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('parses strings with escaped characters', async () => {
        const tmp = path.join(__dirname, '_strict_escapes.tmp');
        try {
            await writeFile(tmp, `{"s": "tab\\tend\\nnewline\\\\backslash\\\"quote"}`, 'utf-8');
            const data = await readJsonFile(tmp) as { s: string };
            expect(data.s).toBe('tab\tend\nnewline\\backslash"quote');
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });
});

// ============================================================
// SECTION: Operations on JSON5-parsed data
// Ensures query/filter/aggregation work on relaxed-JSON input.
// ============================================================
describe('Operations on JSON5-parsed data', () => {
    beforeEach(() => clearCache());

    it('sorts an array of objects from a JSON5 file', async () => {
        const fixture = path.join(__dirname, 'mock-json5.json5');
        const data = await readJsonFile(fixture) as { trailingArray: number[] };
        // The sort extension requires a named field (arrays of objects).
        const items = data.trailingArray.map(v => ({ val: v }));
        const sorted = handleArrayOperations(items, '.sort(-val)') as Array<{ val: number }>;
        expect(sorted.map(x => x.val)).toEqual([3, 2, 1]);
    });

    it('aggregates numeric fields from a JSON5 file', async () => {
        const fixture = path.join(__dirname, 'mock-json5.json5');
        const data = await readJsonFile(fixture) as { trailingArray: number[] };
        // Build objects with a field to aggregate
        const items = data.trailingArray.map(v => ({ val: v }));
        const sum = handleAggregation(items, '.sum(val)');
        expect(sum).toBe(6);
    });

    it('filters a nested array from a JSON5 file', async () => {
        const fixture = path.join(__dirname, 'mock-json5.json5');
        const data = await readJsonFile(fixture) as {
            nested: { level1: { level2: { level3: { level4: { level5: { data: number[] } } } } } };
        };
        const deepData = data.nested.level1.level2.level3.level4.level5.data;
        const result = handleComplexFilter(
            deepData.map(v => ({ v })),
            '@.v > 2'
        ) as Array<{ v: number }>;
        expect(result).toHaveLength(3);
        expect(result.map(x => x.v)).toEqual([3, 4, 5]);
    });
});

// ============================================================
// SECTION: Malformed input rejection
// ============================================================
describe('Malformed input rejection', () => {
    beforeEach(() => clearCache());

    it('rejects genuinely malformed JSON', async () => {
        const tmp = path.join(__dirname, '_malformed.tmp');
        try {
            await writeFile(tmp, `{ broken: "missing closing quote }`, 'utf-8');
            await expect(readJsonFile(tmp)).rejects.toThrow(/Failed to read or parse/);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('rejects empty file', async () => {
        const tmp = path.join(__dirname, '_empty_file.tmp');
        try {
            await writeFile(tmp, '', 'utf-8');
            await expect(readJsonFile(tmp)).rejects.toThrow(/Failed to read or parse/);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });

    it('rejects non-JSON text', async () => {
        const tmp = path.join(__dirname, '_notjson.tmp');
        try {
            await writeFile(tmp, 'hello world this is not json', 'utf-8');
            await expect(readJsonFile(tmp)).rejects.toThrow(/Failed to read or parse/);
        } finally {
            await unlink(tmp).catch(() => {});
        }
    });
});

// ============================================================
// SECTION: resolveQuery dispatch logic
// Directly tests the routing in server.ts that dispatches an
// extended JSONPath expression to the correct handler. Covers
// every branch: length, aggregation, numeric, date, array,
// string, and the plain JSONPath fallback.
// ============================================================
describe('resolveQuery dispatch', () => {
    const data = {
        store: {
            book: [
                { title: "A", price: 8.95, category: "reference", date: "2024-01-01" },
                { title: "B", price: 12.99, category: "fiction", date: "2024-06-15" },
                { title: "C", price: 22.99, category: "fiction", date: "2024-12-31" },
            ],
            bicycle: { color: "red", price: 19.95 },
        },
        nums: [1, 2, 3, 4, 5],
        labels: ["banana", "apple", "cherry"],
    };

    it('handles $.length() on an object (key count)', () => {
        const result = resolveQuery(data as any, "$.length()");
        expect(result).toBe(3); // store, nums, labels
    });

    it('handles $.length() on an array', () => {
        const result = resolveQuery(data.nums as any, "$.length()");
        expect(result).toBe(5);
    });

    it('dispatches .sum() aggregation', () => {
        const result = resolveQuery(data as any, "$.store.book.sum(price)");
        expect(result).toBeCloseTo(44.93, 2);
    });

    it('dispatches .avg() aggregation', () => {
        const result = resolveQuery(data as any, "$.store.book.avg(price)");
        expect(result).toBeCloseTo(14.976666666666667, 2);
    });

    it('dispatches .min() aggregation', () => {
        const result = resolveQuery(data as any, "$.store.book.min(price)");
        expect(result).toBe(8.95);
    });

    it('dispatches .max() aggregation', () => {
        const result = resolveQuery(data as any, "$.store.book.max(price)");
        expect(result).toBe(22.99);
    });

    it('dispatches .math() numeric operation', () => {
        const result = resolveQuery(data as any, "$.nums.math(*2)");
        expect(result).toEqual([2, 4, 6, 8, 10]);
    });

    it('dispatches .round() numeric operation', () => {
        const result = resolveQuery(data as any, "$.store.book[*].price.round()");
        expect(Array.isArray(result)).toBe(true);
    });

    it('dispatches .sqrt() numeric operation', () => {
        const result = resolveQuery(data as any, "$.nums.sqrt()");
        expect(result).toEqual([1, Math.sqrt(2), Math.sqrt(3), 2, Math.sqrt(5)]);
    });

    it('dispatches .format() date operation', () => {
        const result = resolveQuery(data as any, "$.store.book[*].date.format('YYYY-MM-DD')");
        expect(Array.isArray(result)).toBe(true);
        expect((result as string[])[0]).toBe("2024-01-01");
    });

    it('dispatches .isToday() date operation', () => {
        const result = resolveQuery(data as any, "$.store.book[*].date.isToday()");
        expect(Array.isArray(result)).toBe(true);
        expect((result as boolean[]).every(v => v === false)).toBe(true);
    });

    it('dispatches .sort() array operation', () => {
        const result = resolveQuery(data as any, "$.store.book.sort(price)") as any[];
        expect(result[0].price).toBe(8.95);
        expect(result[2].price).toBe(22.99);
    });

    it('dispatches .sort(-field) descending array operation', () => {
        const result = resolveQuery(data as any, "$.store.book.sort(-price)") as any[];
        expect(result[0].price).toBe(22.99);
    });

    it('dispatches .distinct() array operation', () => {
        const result = resolveQuery(data as any, "$.store.book[*].category.distinct()");
        expect(result).toEqual(["reference", "fiction"]);
    });

    it('dispatches .reverse() array operation', () => {
        const result = resolveQuery(data as any, "$.nums.reverse()");
        expect(result).toEqual([5, 4, 3, 2, 1]);
    });

    it('dispatches [start:end] slice array operation', () => {
        const result = resolveQuery(data as any, "$.nums[1:3]");
        expect(result).toEqual([2, 3]);
    });

    it('dispatches .toLowerCase() string operation', () => {
        const result = resolveQuery(data as any, "$.store.bicycle.color.toLowerCase()");
        expect(result).toBe("red");
    });

    it('dispatches .toUpperCase() string operation', () => {
        const result = resolveQuery(data as any, "$.store.bicycle.color.toUpperCase()");
        expect(result).toBe("RED");
    });

    it('dispatches .contains() string operation', () => {
        const result = resolveQuery(data as any, "$.store.bicycle.color.contains('e')");
        expect(result).toBe(true);
    });

    it('dispatches .startsWith() string operation', () => {
        const result = resolveQuery(data as any, "$.store.bicycle.color.startsWith('r')");
        expect(result).toBe(true);
    });

    it('dispatches .matches() string operation', () => {
        const result = resolveQuery(data as any, "$.store.bicycle.color.matches('^r.d$')");
        expect(result).toBe(true);
    });

    it('falls back to plain JSONPath for standard expressions', () => {
        const result = resolveQuery(data as any, "$.store.book[*].title");
        expect(result).toEqual(["A", "B", "C"]);
    });

    it('falls back to plain JSONPath for nested property access', () => {
        // The fallback uses wrap:true, so single results are wrapped in an array.
        const result = resolveQuery(data as any, "$.store.bicycle.color");
        expect(result).toEqual(["red"]);
    });

    it('handles $ basePath for aggregation on a top-level array', () => {
        const result = resolveQuery(data.nums as any, "$.sum(price)");
        // nums are primitives, not objects — getField returns null → toNumber → 0
        expect(result).toBe(0);
    });

    it('single-index [0] falls through to plain JSONPath (not intercepted as slice)', () => {
        const result = resolveQuery(data as any, "$.nums[0]");
        expect(result).toEqual([1]);
    });

    it('single-index [2] falls through to plain JSONPath', () => {
        const result = resolveQuery(data as any, "$.nums[2]");
        expect(result).toEqual([3]);
    });

    it('chained single-index + property falls through to plain JSONPath', () => {
        const result = resolveQuery(data as any, "$.store.book[0].title");
        expect(result).toEqual(["A"]);
    });

    it('slice [1:3] is intercepted by array operations', () => {
        const result = resolveQuery(data as any, "$.nums[1:3]");
        expect(result).toEqual([2, 3]);
    });

    it('string operation on multiple matches (array) applies to each', () => {
        const result = resolveQuery(data as any, "$.store.book[*].title.toUpperCase()");
        expect(result).toEqual(["A", "B", "C"]);
    });

    it('string .contains() on multiple matches applies to each', () => {
        const result = resolveQuery(data as any, "$.store.book[*].category.contains('fic')");
        expect(result).toEqual([false, true, true]);
    });

    it('distinct() does not crash on strings starting with { or [ that are not valid JSON', () => {
        const data2 = { arr: ["{not valid json", "a", "b", "a", "{not valid json"] };
        const result = resolveQuery(data2 as any, "$.arr.distinct()");
        expect(result).toEqual(["{not valid json", "a", "b"]);
    });

    it('distinct() correctly deduplicates objects', () => {
        const data2 = { arr: [{ x: 1 }, { x: 2 }, { x: 1 }] };
        const result = resolveQuery(data2 as any, "$.arr.distinct()");
        expect(result).toEqual([{ x: 1 }, { x: 2 }]);
    });

    it('distinct() handles mixed objects and strings starting with [', () => {
        const data2 = { arr: [{ x: 1 }, "[looks like json", { x: 1 }, "string", "[looks like json"] };
        const result = resolveQuery(data2 as any, "$.arr.distinct()");
        expect(result).toEqual([{ x: 1 }, "[looks like json", "string"]);
    });
});

// ============================================================
// SECTION: Regex regression tests
// Verifies that pre-existing regex false-positive bugs stay fixed.
// These bugs allowed property keys like "ceiling" or "sortorder" to
// incorrectly trigger operation dispatch (e.g. .ceil() matching
// "ceiling"), and restricted field names to \w+ (no nested paths).
// ============================================================
describe('Regex dispatch false-positive regressions', () => {
    const data = {
        // Keys that contain operation keywords as substrings
        ceiling: 42,
        roundtable: 7,
        absolute: -5,
        sqrtdata: 100,
        math: 99,
        sortorder: "asc",
        reversed: [3, 1, 2],
        distinctness: "high",
        format: "plain",
        matches: ["a", "b"],
        // Normal data for positive cases
        nums: [1, 2, 3],
        items: [
            { name: "X", price: 10, user: { discount: 2 } },
            { name: "Y", price: 20, user: { discount: 5 } },
        ],
    };

    it('does NOT dispatch .ceil() for a key named "ceiling"', () => {
        // Should fall through to plain JSONPath, returning the value
        const result = resolveQuery(data as any, "$.ceiling");
        expect(result).toEqual([42]);
    });

    it('does NOT dispatch .round() for a key named "roundtable"', () => {
        const result = resolveQuery(data as any, "$.roundtable");
        expect(result).toEqual([7]);
    });

    it('does NOT dispatch .abs() for a key named "absolute"', () => {
        const result = resolveQuery(data as any, "$.absolute");
        expect(result).toEqual([-5]);
    });

    it('does NOT dispatch .sqrt() for a key named "sqrtdata"', () => {
        const result = resolveQuery(data as any, "$.sqrtdata");
        expect(result).toEqual([100]);
    });

    it('does NOT dispatch .math() for a key named "math"', () => {
        const result = resolveQuery(data as any, "$.math");
        expect(result).toEqual([99]);
    });

    it('does NOT dispatch .sort() for a key named "sortorder"', () => {
        const result = resolveQuery(data as any, "$.sortorder");
        expect(result).toEqual(["asc"]);
    });

    it('does NOT dispatch .reverse() for a key named "reversed"', () => {
        const result = resolveQuery(data as any, "$.reversed");
        expect(result).toEqual([[3, 1, 2]]);
    });

    it('does NOT dispatch .distinct() for a key named "distinctness"', () => {
        const result = resolveQuery(data as any, "$.distinctness");
        expect(result).toEqual(["high"]);
    });

    it('does NOT dispatch .format() for a key named "format"', () => {
        const result = resolveQuery(data as any, "$.format");
        expect(result).toEqual(["plain"]);
    });

    it('does NOT dispatch .matches() for a key named "matches"', () => {
        const result = resolveQuery(data as any, "$.matches");
        expect(result).toEqual([["a", "b"]]);
    });

    it('DOES dispatch .ceil() when written as a method call', () => {
        const result = resolveQuery(data as any, "$.ceiling.ceil()");
        // 42 already integer — ceil(42) = 42
        expect(result).toEqual([42]);
    });

    it('DOES dispatch .sort() when written as a method call', () => {
        const result = resolveQuery(data as any, "$.items.sort(price)") as any[];
        expect(result[0].name).toBe("X");
        expect(result[1].name).toBe("Y");
    });

    it('does NOT dispatch .format() for a key named "formation" (prefix collision)', () => {
        const data2 = { formation: "stratified" };
        const result = resolveQuery(data2 as any, "$.formation");
        expect(result).toEqual(["stratified"]);
    });

    it('does NOT dispatch .sum() for a key named "summary" (prefix collision)', () => {
        const data2 = { summary: "brief" };
        const result = resolveQuery(data2 as any, "$.summary");
        expect(result).toEqual(["brief"]);
    });
});

describe('Nested field path support', () => {
    const data = {
        items: [
            { name: "X", price: 10, user: { discount: 2 } },
            { name: "Y", price: 20, user: { discount: 5 } },
            { name: "Z", price: 15, user: { discount: 0 } },
        ],
    };

    it('sorts by nested field path (user.discount)', () => {
        const result = resolveQuery(data as any, "$.items.sort(user.discount)") as any[];
        expect(result.map(i => i.name)).toEqual(["Z", "X", "Y"]);
    });

    it('sorts by nested field path descending (-user.discount)', () => {
        const result = resolveQuery(data as any, "$.items.sort(-user.discount)") as any[];
        expect(result.map(i => i.name)).toEqual(["Y", "X", "Z"]);
    });

    it('sums nested field path (user.discount)', () => {
        const result = resolveQuery(data as any, "$.items.sum(user.discount)");
        expect(result).toBe(7);
    });

    it('averages nested field path (user.discount)', () => {
        const result = resolveQuery(data as any, "$.items.avg(user.discount)");
        expect(result).toBeCloseTo(7 / 3, 5);
    });

    it('finds min of nested field path (user.discount)', () => {
        const result = resolveQuery(data as any, "$.items.min(user.discount)");
        expect(result).toBe(0);
    });

    it('finds max of nested field path (user.discount)', () => {
        const result = resolveQuery(data as any, "$.items.max(user.discount)");
        expect(result).toBe(5);
    });
});

describe('Math expression with parentheses', () => {
    it('evaluates .math((2+3)*4) applied to a number', () => {
        // num * (2+3) * 4 — with implicit multiplication before (
        const result = resolveQuery([1] as any, "$.math((2+3)*4)");
        expect(result).toEqual([20]);
    });

    it('evaluates .math((10-3)*2) applied to a number', () => {
        const result = resolveQuery([1] as any, "$.math((10-3)*2)");
        expect(result).toEqual([14]);
    });

    it('evaluates .math(*(2+3)) with explicit *', () => {
        const result = resolveQuery([5] as any, "$.math(*(2+3))");
        expect(result).toEqual([25]);
    });

    it('still evaluates simple .math(*2) without parens', () => {
        const result = resolveQuery([10] as any, "$.math(*2)");
        expect(result).toEqual([20]);
    });

    it('evaluates .math((2+3)(4+5)) with )( implicit multiplication', () => {
        // num * (2+3) * (4+5) = 5 * 5 * 9 = 225
        const result = resolveQuery([5] as any, "$.math((2+3)(4+5))");
        expect(result).toEqual([225]);
    });
});

describe('Filter operator validation', () => {
    const items = [
        { price: 10, category: "fiction" },
        { price: 20, category: "reference" },
    ];

    it('rejects bare = as an operator (not a valid comparison)', () => {
        // A bare '=' should not match — the filter returns false for all items
        // because compMatch doesn't match, so the filter returns empty.
        const result = handleComplexFilter(items as any, '@.price = 10');
        expect(result).toEqual([]);
    });

    it('correctly filters with == operator', () => {
        const result = handleComplexFilter(items as any, '@.price == 10');
        expect(result).toHaveLength(1);
        expect((result[0] as any).price).toBe(10);
    });

    it('correctly filters with != operator', () => {
        const result = handleComplexFilter(items as any, '@.category != "fiction"');
        expect(result).toHaveLength(1);
        expect((result[0] as any).category).toBe("reference");
    });

    it('correctly filters with >= operator (not split into >)', () => {
        const result = handleComplexFilter(items as any, '@.price >= 10');
        expect(result).toHaveLength(2);
    });

    it('correctly filters with <= operator (not split into <)', () => {
        const result = handleComplexFilter(items as any, '@.price <= 10');
        expect(result).toHaveLength(1);
        expect((result[0] as any).price).toBe(10);
    });

    it('filters with nested field path (user.discount > 3)', () => {
        const nestedItems = [
            { name: "X", user: { discount: 2 } },
            { name: "Y", user: { discount: 5 } },
            { name: "Z", user: { discount: 0 } },
        ];
        const result = handleComplexFilter(nestedItems as any, '@.user.discount > 3');
        expect(result).toHaveLength(1);
        expect((result[0] as any).name).toBe("Y");
    });

    it('filters with nested field path and == operator', () => {
        const nestedItems = [
            { name: "X", user: { discount: 2 } },
            { name: "Y", user: { discount: 5 } },
        ];
        const result = handleComplexFilter(nestedItems as any, '@.user.discount == 5');
        expect(result).toHaveLength(1);
        expect((result[0] as any).name).toBe("Y");
    });

    it('filters with nested field path + .contains() string operation', () => {
        const nestedItems = [
            { name: "Alice", profile: { bio: "Software engineer" } },
            { name: "Bob", profile: { bio: "Designer" } },
            { name: "Carol", profile: { bio: "Software architect" } },
        ];
        const result = handleComplexFilter(nestedItems as any, '@.profile.bio.contains("Software")');
        expect(result).toHaveLength(2);
        expect(result.map((i: any) => i.name)).toEqual(["Alice", "Carol"]);
    });

    it('filters with nested field path + .startsWith() string operation', () => {
        const nestedItems = [
            { name: "Alice", profile: { role: "admin" } },
            { name: "Bob", profile: { role: "user" } },
        ];
        const result = handleComplexFilter(nestedItems as any, '@.profile.role.startsWith("admin")');
        expect(result).toHaveLength(1);
        expect((result[0] as any).name).toBe("Alice");
    });
});


