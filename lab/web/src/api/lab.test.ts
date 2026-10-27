import { describe, expect, it } from 'vitest';

import { parseLabState } from './lab';

describe('parseLabState', () => {
	it('reads an idle lab', () => {
		expect(parseLabState({ status: 'idle' })).toEqual({ status: 'idle' });
	});

	it('reads a running round into camelCase with a real date', () => {
		const state = parseLabState({
			status: 'running',
			attack: 'clock',
			scenario: 'clock-skew',
			duration_seconds: 300,
			started_at: '2026-10-19T11:35:33Z',
		});

		expect(state).toEqual({
			status: 'running',
			round: null,
			attack: 'clock',
			scenario: 'clock-skew',
			durationSeconds: 300,
			startedAt: new Date('2026-10-19T11:35:33Z'),
		});
	});

	it('refuses a round with missing or broken fields', () => {
		expect(parseLabState({ status: 'running', attack: 'clock' })).toBeNull();
		expect(parseLabState({ status: 'running', attack: 'clock', scenario: 's', duration_seconds: 1, started_at: 'nope' })).toBeNull();
		expect(parseLabState({ status: 'exploding' })).toBeNull();
		expect(parseLabState(null)).toBeNull();
	});
});
