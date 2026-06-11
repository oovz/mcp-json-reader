import { JsonValue, JsonArray, JsonObject } from "./types.js";
import { safeEvaluateMath } from "./math.js";

export function isRecord(value: JsonValue): value is JsonObject {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function toNumber(value: JsonValue): number {
    if (typeof value === 'number') return value;
    if (typeof value === 'string') return Number(value) || 0;
    return 0;
}

export function getField(item: JsonValue, field: string): JsonValue {
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
                return safeEvaluateMath(num, expr);
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
