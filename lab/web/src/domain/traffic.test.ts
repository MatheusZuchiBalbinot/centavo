import { describe, expect, it } from 'vitest';

import { aSecond } from '../test/fixtures';
import { trafficAt } from './traffic';

describe('trafficAt', () => {
	const seconds = [aSecond(0, 20), aSecond(1, 22), aSecond(2, 6)];

	it('skips the second still filling up while the round runs', () => {
		expect(trafficAt(seconds, 2.4, true).ok).toBe(22);
	});

	it('reads every second of a finished round, at the moment shown', () => {
		expect(trafficAt(seconds, 2.4, false).ok).toBe(6);
		expect(trafficAt(seconds, 0.5, false).ok).toBe(20);
		expect(trafficAt([], 3, true)).toEqual({ ok: 0, fail: 0, info: 0 });
	});
});
