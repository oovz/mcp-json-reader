import { describe, it, expect, beforeEach } from 'vitest';
import { handleArrayOperations, handleAggregation, handleNumericOperations, handleComplexFilter } from '../src/index.js';

describe('mcp-json-reader unit tests', () => {
    const mockBooks = [
        { title: "Sayings of the Century", price: 8.95, category: "reference" },
        { title: "Sword of Honour", price: 12.99, category: "fiction" },
        { title: "Moby Dick", price: 8.99, category: "fiction" },
        { title: "The Lord of the Rings", price: 22.99, category: "fiction" }
    ];

    it('handleArrayOperations - sort', () => {
        const result = handleArrayOperations(mockBooks, '.sort(price)');
        expect(result[0].price).toBe(8.95);
        expect(result[3].price).toBe(22.99);
    });

    it('handleArrayOperations - sort desc', () => {
        const result = handleArrayOperations(mockBooks, '.sort(-price)');
        expect(result[0].price).toBe(22.99);
        expect(result[3].price).toBe(8.95);
    });

    it('handleArrayOperations - slice', () => {
        const result = handleArrayOperations(mockBooks, '[1:3]');
        expect(result).toHaveLength(2);
        expect(result[0].title).toBe("Sword of Honour");
    });

    it('handleAggregation - sum', () => {
        const result = handleAggregation(mockBooks, '.sum(price)');
        expect(result).toBeCloseTo(53.92, 2);
    });

    it('handleAggregation - avg', () => {
        const result = handleAggregation(mockBooks, '.avg(price)');
        expect(result).toBeCloseTo(13.48, 2); // 53.92 / 4 = 13.48
    });

    it('handleNumericOperations - math', () => {
        const prices = [10, 20];
        const result = handleNumericOperations(prices, '.math(* 1.1)');
        expect(result).toEqual([11, 22]);
    });

    it('handleComplexFilter - comparison', () => {
        const result = handleComplexFilter(mockBooks, '@.price > 10');
        expect(result).toHaveLength(2);
    });

    it('handleComplexFilter - contains', () => {
        const result = handleComplexFilter(mockBooks, "@.title.contains('Moby')");
        expect(result).toHaveLength(1);
        expect(result[0].title).toBe("Moby Dick");
    });
});
