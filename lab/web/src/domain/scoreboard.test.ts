import { describe, expect, it } from 'vitest';

import type { RoundId } from '../api/ids';
import type { RoundSummary, Verdict } from '../api/rounds';
import { scoreboardOf } from './scoreboard';

const quiet = {
	kills: 0,
	failpointFires: 0,
	networkEpisodes: 0,
	databaseFaults: 0,
	freezes: 0,
	failovers: 0,
	clockSkews: 0,
	partnerLies: 0,
};

function aRound(round: string, verdict: Verdict | null, defenses: string, kills = 0): RoundSummary {
	const counters = { ...quiet, kills };

	return {
		round: round as RoundId,
		scenario: 'random-kills',
		verdict,
		seconds: 180,
		operations: 900,
		counters,
		defenses,
	};
}

describe('the scoreboard', () => {
	it('counts the robust passes since the last robust break, past controls and inconclusive rounds', () => {
		const rounds = [
			aRound('r0009', 'pass', 'robust'),
			aRound('r0008', 'fail', 'robust,-fencing'),
			aRound('r0007', 'inconclusive', 'robust'),
			aRound('r0006', 'pass', 'robust'),
			aRound('r0005', 'fail', 'robust'),
			aRound('r0004', 'pass', 'robust'),
		];

		expect(scoreboardOf(rounds).streak).toBe(2);
	});

	it('keeps the records of robust rounds that passed only', () => {
		const rounds = [aRound('r0003', 'pass', 'robust', 4), aRound('r0002', 'fail', 'robust,-fencing', 9), aRound('r0001', 'pass', 'robust', 7)];
		const board = scoreboardOf(rounds);

		expect(board.mostKills).toEqual({ round: 'r0001', count: 7 });
		expect(board.mostAttacks).toEqual({ round: 'r0001', count: 7 });
	});

	it('has nothing to show without robust rounds', () => {
		expect(scoreboardOf([aRound('r0001', 'pass', '')])).toEqual({ streak: 0, mostAttacks: null, mostKills: null });
	});
});
