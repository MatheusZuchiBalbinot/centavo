import { describe, expect, it } from 'vitest';

import type { RoundId } from '../api/ids';
import { betOutcome, betScore, isBetBook, withBet } from './bets';

describe('bets', () => {
	it('settles a bet by the verdict, and leaves an inconclusive round open', () => {
		expect(betOutcome('holds', 'pass')).toBe('right');
		expect(betOutcome('breaks', 'pass')).toBe('wrong');
		expect(betOutcome('breaks', 'fail')).toBe('right');
		expect(betOutcome('holds', 'inconclusive')).toBeNull();
	});

	it('keeps the first bet on a round and counts the score', () => {
		const book = withBet(withBet(withBet({}, 'r0001' as RoundId, 'holds'), 'r0001' as RoundId, 'breaks'), 'r0002' as RoundId, 'breaks');
		const verdicts = new Map([
			['r0001', 'pass' as const],
			['r0002', 'pass' as const],
		]);

		expect(book).toEqual({ r0001: 'holds', r0002: 'breaks' });
		expect(betScore(book, verdicts)).toEqual({ right: 1, wrong: 1 });
	});

	it('reads back only a well-formed book', () => {
		expect(isBetBook({ r0001: 'holds' })).toBe(true);
		expect(isBetBook({ r0001: 'maybe' })).toBe(false);
		expect(isBetBook(['holds'])).toBe(false);
	});
});
