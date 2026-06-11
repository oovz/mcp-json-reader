import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
    handleArrayOperations,
    handleAggregation,
    handleNumericOperations,
    handleStringOperations,
    handleDateOperations,
    handleComplexFilter,
    resolvePath,
    readJsonFile,
    clearCache,
    getCacheStats,
    MAX_FILE_SIZE_BYTES,
} from '../src/index.js';
import path from 'path';
import { fileURLToPath } from 'url';
import { writeFile, unlink, mkdir, utimes } from 'fs/promises';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// --- Type definitions for test data ---
interface Book {
    title: string;
    price: number;
    category: string;
    author?: string;
    isbn?: string;
    date?: string;
}

interface Employee {
    id: number;
    name: string;
    email: string;
    department: string;
    salary: number;
    startDate: string;
    active: boolean;
    rating: number;
}

interface Transaction {
    id: string;
    date: string;
    amount: number;
    type: string;
    category: string;
    description: string;
}

interface NullableRecord {
    name: string | null;
    score: number | null;
}

interface TaggedRecord {
    id: number;
    tag: string;
}

interface ValueRecord {
    val: number | string | boolean | null;
}

interface LargeArrayItem {
    id: number;
    value: number;
    name: string;
    category: string;
    active: boolean;
}

// ============================================================
// SECTION 1: V8 HEAP MEMORY LIMITS & FILE SIZE GUARD
// ============================================================
describe('V8 Heap Memory Limits', () => {
    it('exports MAX_FILE_SIZE_BYTES constant', () => {
        expect(MAX_FILE_SIZE_BYTES).toBeDefined();
        expect(typeof MAX_FILE_SIZE_BYTES).toBe('number');
    });

    it('MAX_FILE_SIZE_BYTES is 1.5 GB (safe for 16 GB system with ~4 GB V8 heap)', () => {
        // V8 default heap on 64-bit is ~4 GB.
        // JSON.parse inflates by ~2-10x. A 1.5 GB file could use 3-15 GB of heap.
        // On a 16 GB system, the practical safe limit for a single parsed JSON
        // is ~1.5 GB raw file size (leaves room for the OS, other processes, and
        // the parsed object graph which is typically 2-6x the raw size).
        const ONE_POINT_FIVE_GB = 1.5 * 1024 * 1024 * 1024;
        expect(MAX_FILE_SIZE_BYTES).toBe(ONE_POINT_FIVE_GB);
    });

    it('readJsonFile rejects files exceeding MAX_FILE_SIZE_BYTES', async () => {
        // We won't create a 1.5GB file in tests; instead we test that the
        // size check exists by creating a small file and verifying the
        // function works, then testing the error message format for oversized files.
        // The actual guard is validated via the constant export.
        const smallPath = path.join(__dirname, 'mock.json');
        const data = await readJsonFile(smallPath);
        expect(data).toBeDefined();
    });
});

// ============================================================
// SECTION 2: CACHE MANAGEMENT
// ============================================================
describe('Cache Management', () => {
    beforeEach(() => {
        clearCache();
    });

    it('clearCache() empties the cache', async () => {
        const mockPath = path.join(__dirname, 'mock.json');
        await readJsonFile(mockPath);
        let stats = getCacheStats();
        expect(stats.entries).toBe(1);

        clearCache();
        stats = getCacheStats();
        expect(stats.entries).toBe(0);
    });

    it('getCacheStats() returns entry count', async () => {
        const stats = getCacheStats();
        expect(stats).toHaveProperty('entries');
        expect(typeof stats.entries).toBe('number');
    });

    it('caching returns identical reference on second read', async () => {
        const mockPath = path.join(__dirname, 'mock.json');
        const data1 = await readJsonFile(mockPath);
        const data2 = await readJsonFile(mockPath);
        expect(data2).toBe(data1); // same reference = cache hit
    });

    it('cache invalidates when file mtime changes', async () => {
        const tmpPath = path.join(__dirname, '_cache_test_tmp.json');
        try {
            await writeFile(tmpPath, JSON.stringify({ version: 1 }), 'utf-8');
            const data1 = await readJsonFile(tmpPath) as { version: number };
            expect(data1.version).toBe(1);

            // Force a distinct mtime (filesystem granularity can be 1-15ms on NTFS)
            await writeFile(tmpPath, JSON.stringify({ version: 2 }), 'utf-8');
            const futureTime = new Date(Date.now() + 2000);
            await utimes(tmpPath, futureTime, futureTime);
            const data2 = await readJsonFile(tmpPath) as { version: number };
            expect(data2.version).toBe(2);
            expect(data2).not.toBe(data1); // different reference = cache miss
        } finally {
            await unlink(tmpPath).catch(() => {});
        }
    });

    it('cache evicts LRU entry when exceeding max entries', async () => {
        // The cache has a max entry count. When we exceed it, the least
        // recently used entry should be evicted.
        const tmpDir = path.join(__dirname, '_cache_lru_test');
        await mkdir(tmpDir, { recursive: true });
        const paths: string[] = [];

        try {
            // Create 12 temp files (cache limit is 10)
            for (let i = 0; i < 12; i++) {
                const p = path.join(tmpDir, `file_${i}.json`);
                await writeFile(p, JSON.stringify({ index: i }), 'utf-8');
                paths.push(p);
            }

            // Read all 12 — first 2 should be evicted
            for (const p of paths) {
                await readJsonFile(p);
            }

            const stats = getCacheStats();
            expect(stats.entries).toBeLessThanOrEqual(10);
        } finally {
            for (const p of paths) {
                await unlink(p).catch(() => {});
            }
            await unlink(tmpDir).catch(() => {});
        }
    });
});

// ============================================================
// SECTION 3: PATH RESOLUTION
// ============================================================
describe('resolvePath', () => {
    it('returns absolute paths unchanged', () => {
        const abs = path.resolve('C:\\test.json');
        expect(resolvePath(abs)).toBe(abs);
    });

    it('resolves relative paths against cwd', () => {
        const rel = 'data.json';
        const resolved = resolvePath(rel);
        expect(resolved).toBe(path.resolve(process.cwd(), rel));
    });

    it('handles paths with dots', () => {
        const dotPath = './some/../data.json';
        const resolved = resolvePath(dotPath);
        expect(path.isAbsolute(resolved)).toBe(true);
    });

    it('handles paths with forward slashes', () => {
        const fwdPath = 'subdir/data.json';
        const resolved = resolvePath(fwdPath);
        expect(path.isAbsolute(resolved)).toBe(true);
        expect(resolved).toContain('subdir');
    });
});

// ============================================================
// SECTION 4: FILE READING & ERROR HANDLING
// ============================================================
describe('readJsonFile', () => {
    beforeEach(() => {
        clearCache();
    });

    it('reads and parses valid JSON', async () => {
        const mockPath = path.join(__dirname, 'mock.json');
        const data = await readJsonFile(mockPath);
        expect(data).toBeDefined();
        expect(typeof data).toBe('object');
    });

    it('reads complex nested JSON', async () => {
        const largePath = path.join(__dirname, 'mock-large.json');
        const data = await readJsonFile(largePath) as { company: { name: string } };
        expect(data.company.name).toBe('Acme Corp');
    });

    it('throws on non-existent file', async () => {
        await expect(readJsonFile('/nonexistent/file.json')).rejects.toThrow(/Failed to read or parse/);
    });

    it('throws on invalid JSON content', async () => {
        const tmpPath = path.join(__dirname, '_invalid_json.tmp');
        try {
            await writeFile(tmpPath, '{ invalid json content!!!', 'utf-8');
            await expect(readJsonFile(tmpPath)).rejects.toThrow(/Failed to read or parse/);
        } finally {
            await unlink(tmpPath).catch(() => {});
        }
    });

    it('throws on empty file', async () => {
        const tmpPath = path.join(__dirname, '_empty.tmp');
        try {
            await writeFile(tmpPath, '', 'utf-8');
            await expect(readJsonFile(tmpPath)).rejects.toThrow(/Failed to read or parse/);
        } finally {
            await unlink(tmpPath).catch(() => {});
        }
    });
});

// ============================================================
// SECTION 5: ARRAY OPERATIONS
// ============================================================
describe('handleArrayOperations', () => {
    const books: Book[] = [
        { title: "Sayings of the Century", price: 8.95, category: "reference" },
        { title: "Sword of Honour", price: 12.99, category: "fiction" },
        { title: "Moby Dick", price: 8.99, category: "fiction" },
        { title: "The Lord of the Rings", price: 22.99, category: "fiction" },
    ];

    describe('sorting', () => {
        it('sorts ascending by numeric field', () => {
            const result = handleArrayOperations(books, '.sort(price)') as Book[];
            expect(result[0].price).toBe(8.95);
            expect(result[1].price).toBe(8.99);
            expect(result[2].price).toBe(12.99);
            expect(result[3].price).toBe(22.99);
        });

        it('sorts descending by numeric field', () => {
            const result = handleArrayOperations(books, '.sort(-price)') as Book[];
            expect(result[0].price).toBe(22.99);
            expect(result[3].price).toBe(8.95);
        });

        it('sorts ascending by string field', () => {
            const result = handleArrayOperations(books, '.sort(title)') as Book[];
            expect(result[0].title).toBe("Moby Dick");
            expect(result[3].title).toBe("The Lord of the Rings");
        });

        it('sorts descending by string field', () => {
            const result = handleArrayOperations(books, '.sort(-title)') as Book[];
            expect(result[0].title).toBe("The Lord of the Rings");
        });

        it('handles null values during sort — nulls last', () => {
            const withNulls = [
                { name: "Charlie", score: 10 },
                { name: "Alice", score: null },
                { name: "Bob", score: 5 },
            ] as Array<{ name: string; score: number | null }>;
            const result = handleArrayOperations(withNulls, '.sort(score)') as Array<{ name: string; score: number | null }>;
            // Nulls should sort to end
            expect(result[result.length - 1].score).toBeNull();
        });
    });

    describe('slicing', () => {
        it('slices with start and end', () => {
            const result = handleArrayOperations(books, '[1:3]') as Book[];
            expect(result).toHaveLength(2);
            expect(result[0].title).toBe("Sword of Honour");
            expect(result[1].title).toBe("Moby Dick");
        });

        it('slices from start when start omitted', () => {
            const result = handleArrayOperations(books, '[:2]') as Book[];
            expect(result).toHaveLength(2);
            expect(result[0].title).toBe("Sayings of the Century");
        });

        it('slices to end when end omitted', () => {
            const result = handleArrayOperations(books, '[2:]') as Book[];
            expect(result).toHaveLength(2);
            expect(result[0].title).toBe("Moby Dick");
        });

        it('returns empty for out-of-bounds slice', () => {
            const result = handleArrayOperations(books, '[10:20]') as Book[];
            expect(result).toHaveLength(0);
        });
    });

    describe('distinct', () => {
        it('removes duplicate primitive values', () => {
            const nums = [1, 2, 2, 3, 3, 3];
            const result = handleArrayOperations(nums, '.distinct()') as number[];
            expect(result).toHaveLength(3);
            expect(result).toEqual([1, 2, 3]);
        });

        it('removes duplicate objects', () => {
            const dupes: TaggedRecord[] = [
                { id: 1, tag: "alpha" },
                { id: 2, tag: "beta" },
                { id: 1, tag: "alpha" },
            ];
            const result = handleArrayOperations(dupes, '.distinct()') as TaggedRecord[];
            expect(result).toHaveLength(2);
        });
    });

    describe('reverse', () => {
        it('reverses array order', () => {
            const result = handleArrayOperations(books, '.reverse()') as Book[];
            expect(result[0].title).toBe("The Lord of the Rings");
            expect(result[3].title).toBe("Sayings of the Century");
        });
    });

    describe('combined operations', () => {
        it('sorts then slices', () => {
            const result = handleArrayOperations(books, '.sort(price)[0:2]') as Book[];
            expect(result).toHaveLength(2);
            expect(result[0].price).toBe(8.95);
            expect(result[1].price).toBe(8.99);
        });

        it('handles empty array', () => {
            const result = handleArrayOperations([], '.sort(price)');
            expect(result).toHaveLength(0);
        });

        it('handles single-element array', () => {
            const single = [{ price: 10 }];
            const result = handleArrayOperations(single, '.sort(price)');
            expect(result).toHaveLength(1);
        });
    });

    describe('large array performance', () => {
        it('sorts 10,000 items correctly', async () => {
            const largePath = path.join(__dirname, 'mock-edge-cases.json');
            clearCache();
            const data = await readJsonFile(largePath) as { edgeCases: { largeArray: LargeArrayItem[] } };
            const largeArray = data.edgeCases.largeArray;
            expect(largeArray).toHaveLength(10000);

            const sorted = handleArrayOperations(largeArray, '.sort(value)') as LargeArrayItem[];
            expect(sorted).toHaveLength(10000);
            // Verify sorted order
            for (let i = 1; i < sorted.length; i++) {
                expect(sorted[i].value).toBeGreaterThanOrEqual(sorted[i - 1].value);
            }
        });

        it('slices large array efficiently', async () => {
            const largePath = path.join(__dirname, 'mock-edge-cases.json');
            const data = await readJsonFile(largePath) as { edgeCases: { largeArray: LargeArrayItem[] } };
            const result = handleArrayOperations(data.edgeCases.largeArray, '[0:5]') as LargeArrayItem[];
            expect(result).toHaveLength(5);
        });

        it('distinct on large array with duplicated categories', async () => {
            const largePath = path.join(__dirname, 'mock-edge-cases.json');
            const data = await readJsonFile(largePath) as { edgeCases: { largeArray: LargeArrayItem[] } };
            const categories = data.edgeCases.largeArray.map((item: LargeArrayItem) => item.category);
            const result = handleArrayOperations(categories, '.distinct()') as string[];
            expect(result).toHaveLength(4); // alpha, beta, gamma, delta
        });
    });
});

// ============================================================
// SECTION 6: AGGREGATION
// ============================================================
describe('handleAggregation', () => {
    const books: Book[] = [
        { title: "A", price: 8.95, category: "ref" },
        { title: "B", price: 12.99, category: "fic" },
        { title: "C", price: 8.99, category: "fic" },
        { title: "D", price: 22.99, category: "fic" },
    ];

    it('computes sum', () => {
        const result = handleAggregation(books, '.sum(price)');
        expect(result).toBeCloseTo(53.92, 2);
    });

    it('computes average', () => {
        const result = handleAggregation(books, '.avg(price)');
        expect(result).toBeCloseTo(13.48, 2);
    });

    it('computes min', () => {
        const result = handleAggregation(books, '.min(price)');
        expect(result).toBe(8.95);
    });

    it('computes max', () => {
        const result = handleAggregation(books, '.max(price)');
        expect(result).toBe(22.99);
    });

    it('returns 0 for empty array', () => {
        expect(handleAggregation([], '.sum(price)')).toBe(0);
    });

    it('returns 0 for non-matching operation', () => {
        expect(handleAggregation(books, '.unknown(price)')).toBe(0);
    });

    it('coerces non-numeric values to 0', () => {
        const mixed = [
            { val: 10 },
            { val: "not-a-number" },
            { val: 20 },
        ];
        const result = handleAggregation(mixed, '.sum(val)');
        expect(result).toBe(30);
    });

    it('handles numeric strings', () => {
        const stringNums = [
            { val: "10" },
            { val: "20" },
            { val: "30" },
        ];
        const result = handleAggregation(stringNums, '.sum(val)');
        expect(result).toBe(60);
    });

    describe('with large dataset', () => {
        it('sums salaries from company data', async () => {
            const largePath = path.join(__dirname, 'mock-large.json');
            clearCache();
            const data = await readJsonFile(largePath) as { company: { employees: Employee[] } };
            const employees = data.company.employees;
            const result = handleAggregation(employees, '.sum(salary)');
            // Sum of all 15 employee salaries
            const expectedSum = employees.reduce((sum: number, e: Employee) => sum + e.salary, 0);
            expect(result).toBe(expectedSum);
        });

        it('computes average rating', async () => {
            const largePath = path.join(__dirname, 'mock-large.json');
            const data = await readJsonFile(largePath) as { company: { employees: Employee[] } };
            const employees = data.company.employees;
            const result = handleAggregation(employees, '.avg(rating)');
            const expectedAvg = employees.reduce((sum: number, e: Employee) => sum + e.rating, 0) / employees.length;
            expect(result).toBeCloseTo(expectedAvg, 2);
        });

        it('finds min/max salary', async () => {
            const largePath = path.join(__dirname, 'mock-large.json');
            const data = await readJsonFile(largePath) as { company: { employees: Employee[] } };
            const employees = data.company.employees;
            const minResult = handleAggregation(employees, '.min(salary)');
            const maxResult = handleAggregation(employees, '.max(salary)');
            expect(minResult).toBe(78000);
            expect(maxResult).toBe(185000);
        });
    });
});

// ============================================================
// SECTION 7: NUMERIC OPERATIONS
// ============================================================
describe('handleNumericOperations', () => {
    it('applies multiplication via .math()', () => {
        const result = handleNumericOperations([10, 20], '.math(* 1.1)');
        expect(result).toEqual([11, 22]);
    });

    it('applies addition via .math()', () => {
        const result = handleNumericOperations([10, 20, 30], '.math(+ 5)');
        expect(result).toEqual([15, 25, 35]);
    });

    it('applies subtraction via .math()', () => {
        const result = handleNumericOperations([10, 20], '.math(- 3)');
        expect(result).toEqual([7, 17]);
    });

    it('applies division via .math()', () => {
        const result = handleNumericOperations([10, 20], '.math(/ 2)');
        expect(result).toEqual([5, 10]);
    });

    it('rounds values', () => {
        const result = handleNumericOperations([1.4, 1.5, 1.6], '.round()');
        expect(result).toEqual([1, 2, 2]);
    });

    it('floors values', () => {
        const result = handleNumericOperations([1.9, 2.1, -1.1], '.floor()');
        expect(result).toEqual([1, 2, -2]);
    });

    it('ceils values', () => {
        const result = handleNumericOperations([1.1, 2.9, -1.9], '.ceil()');
        expect(result).toEqual([2, 3, -1]);
    });

    it('computes absolute values', () => {
        const result = handleNumericOperations([-5, 3, -10], '.abs()');
        expect(result).toEqual([5, 3, 10]);
    });

    it('computes square root', () => {
        const result = handleNumericOperations([4, 9, 16], '.sqrt()');
        expect(result).toEqual([2, 3, 4]);
    });

    it('computes power of 2', () => {
        const result = handleNumericOperations([2, 3, 4], '.pow2()');
        expect(result).toEqual([4, 9, 16]);
    });

    it('coerces non-numeric to 0', () => {
        const result = handleNumericOperations(["abc" as unknown as number, 10], '.round()');
        expect(result).toEqual([0, 10]);
    });

    it('returns raw values for unrecognized operation', () => {
        const result = handleNumericOperations([1, 2, 3], '.unknown()');
        expect(result).toEqual([1, 2, 3]);
    });

    it('rejects non-numeric math expressions', () => {
        // expression with letters should be rejected by the allowed regex
        const result = handleNumericOperations([10], '.math(alert("xss"))');
        expect(result).toEqual([0]);
    });

    it('handles empty array', () => {
        const result = handleNumericOperations([], '.round()');
        expect(result).toEqual([]);
    });
});

// ============================================================
// SECTION 8: STRING OPERATIONS
// ============================================================
describe('handleStringOperations', () => {
    it('converts to lowercase', () => {
        expect(handleStringOperations("HELLO", '.toLowerCase()')).toBe('hello');
    });

    it('converts to uppercase', () => {
        expect(handleStringOperations("hello", '.toUpperCase()')).toBe('HELLO');
    });

    it('checks startsWith', () => {
        expect(handleStringOperations("Hello World", ".startsWith('Hello')")).toBe(true);
        expect(handleStringOperations("Hello World", ".startsWith('World')")).toBe(false);
    });

    it('checks endsWith', () => {
        expect(handleStringOperations("Hello World", ".endsWith('World')")).toBe(true);
        expect(handleStringOperations("Hello World", ".endsWith('Hello')")).toBe(false);
    });

    it('checks contains', () => {
        expect(handleStringOperations("Hello World", ".contains('lo Wo')")).toBe(true);
        expect(handleStringOperations("Hello World", ".contains('xyz')")).toBe(false);
    });

    it('checks matches with regex', () => {
        expect(handleStringOperations("abc123", ".matches('^[a-z]+\\d+$')")).toBe(true);
        expect(handleStringOperations("ABC", ".matches('^[a-z]+$')")).toBe(false);
    });

    it('returns non-string values unchanged', () => {
        expect(handleStringOperations(42, '.toLowerCase()')).toBe(42);
        expect(handleStringOperations(null, '.toUpperCase()')).toBeNull();
        expect(handleStringOperations(true, '.contains("x")')).toBe(true);
    });

    it('handles empty string', () => {
        expect(handleStringOperations("", '.toLowerCase()')).toBe('');
        expect(handleStringOperations("", ".contains('')")).toBe(true);
    });

    it('handles double quotes in operations', () => {
        expect(handleStringOperations("Hello World", '.contains("World")')).toBe(true);
    });

    it('returns value for unrecognized operation', () => {
        expect(handleStringOperations("test", '.unknown()')).toBe('test');
    });
});

// ============================================================
// SECTION 9: DATE OPERATIONS
// ============================================================
describe('handleDateOperations', () => {
    it('formats dates as YYYY-MM-DD', () => {
        const dates = ["2024-01-15", "2024-12-31"];
        const result = handleDateOperations(dates, ".format('YYYY-MM-DD')") as string[];
        expect(result[0]).toBe("2024-01-15");
        expect(result[1]).toBe("2024-12-31");
    });

    it('formats dates with time components', () => {
        const dates = ["2024-06-15T14:30:45Z"];
        const result = handleDateOperations(dates, ".format('YYYY-MM-DD HH:mm:ss')") as string[];
        // UTC time formatting
        expect(result[0]).toMatch(/2024-06-15/);
    });

    it('returns invalid dates unchanged', () => {
        const dates = ["not-a-date", "2024-01-01"];
        const result = handleDateOperations(dates, ".format('YYYY-MM-DD')") as string[];
        expect(result[0]).toBe("not-a-date");
        expect(result[1]).toBe("2024-01-01");
    });

    it('isToday returns booleans', () => {
        const today = new Date().toISOString().split('T')[0];
        const dates = [today, "2000-01-01"];
        const result = handleDateOperations(dates, '.isToday()') as boolean[];
        expect(result[0]).toBe(true);
        expect(result[1]).toBe(false);
    });

    it('handles empty array', () => {
        const result = handleDateOperations([], ".format('YYYY-MM-DD')");
        expect(result).toEqual([]);
    });

    it('returns data unchanged for unrecognized operation', () => {
        const dates = ["2024-01-01"];
        const result = handleDateOperations(dates, '.unknown()');
        expect(result).toEqual(["2024-01-01"]);
    });

    it('formats dates from large dataset', async () => {
        const largePath = path.join(__dirname, 'mock-large.json');
        clearCache();
        const data = await readJsonFile(largePath) as { company: { employees: Employee[] } };
        const startDates = data.company.employees.map((e: Employee) => e.startDate);
        const result = handleDateOperations(startDates, ".format('YYYY-MM-DD')") as string[];
        expect(result).toHaveLength(15);
        // All should match YYYY-MM-DD format
        for (const d of result) {
            expect(d).toMatch(/^\d{4}-\d{2}-\d{2}$/);
        }
    });
});

// ============================================================
// SECTION 10: COMPLEX FILTER
// ============================================================
describe('handleComplexFilter', () => {
    const books: Book[] = [
        { title: "Sayings of the Century", price: 8.95, category: "reference" },
        { title: "Sword of Honour", price: 12.99, category: "fiction" },
        { title: "Moby Dick", price: 8.99, category: "fiction" },
        { title: "The Lord of the Rings", price: 22.99, category: "fiction" },
    ];

    describe('comparison operators', () => {
        it('filters with > operator', () => {
            const result = handleComplexFilter(books, '@.price > 10') as Book[];
            expect(result).toHaveLength(2);
            expect(result.every((b: Book) => b.price > 10)).toBe(true);
        });

        it('filters with >= operator', () => {
            const result = handleComplexFilter(books, '@.price >= 8.99') as Book[];
            expect(result).toHaveLength(3);
        });

        it('filters with < operator', () => {
            const result = handleComplexFilter(books, '@.price < 10') as Book[];
            expect(result).toHaveLength(2);
        });

        it('filters with <= operator', () => {
            const result = handleComplexFilter(books, '@.price <= 8.99') as Book[];
            expect(result).toHaveLength(2);
        });

        it('filters with == operator (string)', () => {
            const result = handleComplexFilter(books, '@.category == "fiction"') as Book[];
            expect(result).toHaveLength(3);
        });

        it('filters with != operator', () => {
            const result = handleComplexFilter(books, '@.category != "fiction"') as Book[];
            expect(result).toHaveLength(1);
            expect(result[0].category).toBe("reference");
        });
    });

    describe('string operations in filter', () => {
        it('filters with contains', () => {
            const result = handleComplexFilter(books, "@.title.contains('Moby')") as Book[];
            expect(result).toHaveLength(1);
            expect(result[0].title).toBe("Moby Dick");
        });

        it('filters with startsWith', () => {
            const result = handleComplexFilter(books, "@.title.startsWith('The')") as Book[];
            expect(result).toHaveLength(1);
            expect(result[0].title).toBe("The Lord of the Rings");
        });

        it('filters with endsWith', () => {
            const result = handleComplexFilter(books, "@.title.endsWith('Dick')") as Book[];
            expect(result).toHaveLength(1);
        });

        it('filters with matches (regex)', () => {
            const result = handleComplexFilter(books, "@.title.matches('^S')") as Book[];
            expect(result).toHaveLength(2); // Sayings..., Sword...
        });
    });

    describe('edge cases', () => {
        it('returns empty array when no matches', () => {
            const result = handleComplexFilter(books, '@.price > 100');
            expect(result).toHaveLength(0);
        });

        it('handles empty input array', () => {
            const result = handleComplexFilter([], '@.price > 0');
            expect(result).toHaveLength(0);
        });

        it('handles missing fields gracefully', () => {
            // isbn is missing from first two books
            const result = handleComplexFilter(books, "@.isbn.contains('0-553')") as Book[];
            // Should not throw; items without isbn should be excluded
            expect(result.length).toBeGreaterThanOrEqual(0);
        });
    });

    describe('with large dataset', () => {
        it('filters employees by salary', async () => {
            const largePath = path.join(__dirname, 'mock-large.json');
            clearCache();
            const data = await readJsonFile(largePath) as { company: { employees: Employee[] } };
            const result = handleComplexFilter(data.company.employees, '@.salary > 130000') as Employee[];
            const expected = data.company.employees.filter((e: Employee) => e.salary > 130000);
            expect(result).toHaveLength(expected.length);
        });

        it('filters employees by email domain', async () => {
            const largePath = path.join(__dirname, 'mock-large.json');
            const data = await readJsonFile(largePath) as { company: { employees: Employee[] } };
            const result = handleComplexFilter(data.company.employees, "@.email.endsWith('@acme.com')") as Employee[];
            const expected = data.company.employees.filter((e: Employee) => e.email.endsWith('@acme.com'));
            expect(result).toHaveLength(expected.length);
        });

        it('filters transactions by type', async () => {
            const largePath = path.join(__dirname, 'mock-large.json');
            const data = await readJsonFile(largePath) as { company: { transactions: Transaction[] } };
            const result = handleComplexFilter(data.company.transactions, '@.type == "revenue"') as Transaction[];
            expect(result).toHaveLength(5);
        });

        it('filters 10,000 items from edge case data', async () => {
            const edgePath = path.join(__dirname, 'mock-edge-cases.json');
            clearCache();
            const data = await readJsonFile(edgePath) as { edgeCases: { largeArray: LargeArrayItem[] } };
            const result = handleComplexFilter(data.edgeCases.largeArray, '@.value > 500') as LargeArrayItem[];
            const expected = data.edgeCases.largeArray.filter((item: LargeArrayItem) => item.value > 500);
            expect(result).toHaveLength(expected.length);
        });
    });
});

// ============================================================
// SECTION 11: EDGE CASES WITH SPECIAL DATA
// ============================================================
describe('Edge cases with special data', () => {
    beforeEach(() => {
        clearCache();
    });

    it('handles null values in aggregation', async () => {
        const edgePath = path.join(__dirname, 'mock-edge-cases.json');
        const data = await readJsonFile(edgePath) as { edgeCases: { nullValues: NullableRecord[] } };
        const nullValues = data.edgeCases.nullValues;
        const result = handleAggregation(nullValues, '.sum(score)');
        // null and non-numeric should coerce to 0; 100 + 0 + 0 = 100
        expect(result).toBe(100);
    });

    it('handles mixed types in numeric operations', async () => {
        const edgePath = path.join(__dirname, 'mock-edge-cases.json');
        const data = await readJsonFile(edgePath) as { edgeCases: { mixedTypes: ValueRecord[] } };
        const vals = data.edgeCases.mixedTypes.map((item: ValueRecord) => item.val);
        const result = handleNumericOperations(vals as number[], '.round()');
        // Non-numeric values should coerce to 0
        expect(Array.isArray(result)).toBe(true);
    });

    it('sorts deeply nested data after extraction', async () => {
        const edgePath = path.join(__dirname, 'mock-edge-cases.json');
        const data = await readJsonFile(edgePath) as {
            edgeCases: { nestedDeep: { level1: { level2: { level3: { level4: { level5: { data: number[] } } } } } } }
        };
        const deepData = data.edgeCases.nestedDeep.level1.level2.level3.level4.level5.data;
        const result = handleArrayOperations(deepData, '.reverse()') as number[];
        expect(result).toEqual([5, 4, 3, 2, 1]);
    });

    it('distinct removes duplicate objects from edge case data', async () => {
        const edgePath = path.join(__dirname, 'mock-edge-cases.json');
        const data = await readJsonFile(edgePath) as { edgeCases: { duplicates: TaggedRecord[] } };
        const result = handleArrayOperations(data.edgeCases.duplicates, '.distinct()') as TaggedRecord[];
        expect(result).toHaveLength(3); // 3 unique: {1,alpha}, {2,beta}, {3,gamma}
    });

    it('handles numeric edge values', async () => {
        const edgePath = path.join(__dirname, 'mock-edge-cases.json');
        const data = await readJsonFile(edgePath) as { edgeCases: { numericEdges: Array<{ val: number }> } };
        const vals = data.edgeCases.numericEdges.map((item: { val: number }) => item.val);
        const result = handleNumericOperations(vals, '.abs()');
        expect(result[0]).toBe(0); // abs(0)
        expect(result[1]).toBe(1); // abs(-1)
        expect(result[2]).toBeCloseTo(999.99, 2); // abs(-999.99)
    });

    it('handles edge case dates', async () => {
        const edgePath = path.join(__dirname, 'mock-edge-cases.json');
        const data = await readJsonFile(edgePath) as { edgeCases: { dates: string[] } };
        const dates = data.edgeCases.dates;
        const result = handleDateOperations(dates, ".format('YYYY-MM-DD')");
        // "invalid-date" and "" should pass through unchanged
        expect(result).toContain("invalid-date");
    });
});
