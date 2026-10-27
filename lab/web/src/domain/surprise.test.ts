import { describe, expect, it } from 'vitest';

import type { AttackMenu } from '../api/attacks';
import type { AttackId } from '../api/ids';
import { drawSurprise, reelOf } from './surprise';

const menu: AttackMenu = {
	attacks: [
		{ id: 'calm' as AttackId, scenarios: ['smoke'] },
		{ id: 'workers-down' as AttackId, scenarios: ['kill-after-commit'] },
		{ id: 'network' as AttackId, scenarios: ['slow-network'] },
	],
	durationsSeconds: [180, 300, 600],
};

function sequence(...values: number[]): () => number {
	let index = 0;

	return () => {
		const value = values[index % values.length] ?? 0;
		index += 1;

		return value;
	};
}

describe('the surprise draw', () => {
	it('never draws the quiet round nor the long duration', () => {
		for (let step = 0; step < 20; step += 1) {
			const draw = drawSurprise(menu, sequence(step / 20, 0.99));

			expect(draw?.attack).not.toBe('calm');
			expect(draw?.durationSeconds).not.toBe(600);
		}
	});

	it('draws by the random source it is given', () => {
		expect(drawSurprise(menu, sequence(0, 0))).toEqual({ attack: 'workers-down', durationSeconds: 180 });
		expect(drawSurprise(menu, sequence(0.9, 0.9))).toEqual({ attack: 'network', durationSeconds: 300 });
	});

	it('has nothing to draw from an empty menu', () => {
		expect(drawSurprise({ attacks: [], durationsSeconds: [180] }, Math.random)).toBeNull();
	});

	it('stops the reel on the winner, with cards after it', () => {
		const winner = 'network' as AttackId;
		const reel = reelOf(['workers-down' as AttackId, winner], winner, sequence(0));

		expect(reel.cards[reel.winnerIndex]).toBe(winner);
		expect(reel.cards.length).toBeGreaterThan(reel.winnerIndex + 1);
	});
});
