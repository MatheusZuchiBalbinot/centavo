import { describe, expect, it } from 'vitest';

import { niceTicks, scaleMaxOf } from './scale';

describe('scaleMaxOf', () => {
	it('lets a lone spike go off the top instead of flattening the rest', () => {
		const values = [80, ...Array.from({ length: 179 }, () => 16)];

		expect(scaleMaxOf(values)).toBeCloseTo(16 * 1.15);
		expect(niceTicks(scaleMaxOf(values)).at(-1)).toBe(20);
	});

	it('always fits the latest value, which the chart labels', () => {
		const values = [...Array.from({ length: 99 }, () => 10), 90];

		expect(scaleMaxOf(values)).toBe(90);
	});

	it('is zero without values', () => {
		expect(scaleMaxOf([])).toBe(0);
	});
});
