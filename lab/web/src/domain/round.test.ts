import { describe, expect, it } from 'vitest';

import type { RoundEvent } from '../api/replay';
import { anEvent, aReading, aReplay, aSecond } from '../test/fixtures';
import { feedOf } from './feed';
import { guaranteeView } from './guarantees';
import { expectationOf, headlineOf } from './headline';
import { overlaps, rangeOfLabel } from './highlight';
import { narrate } from './narrator';
import { phaseOf } from './phase';
import { initialRoundState, mergeSeconds, reduceRound } from './roundState';
import { clusterMarkers, spanOf } from './timeline';

describe('reduceRound', () => {
	it('follows a round from its snapshot to its verdict, counting what came after the snapshot', () => {
		const snapshot = reduceRound(initialRoundState, { type: 'snapshot', replay: aReplay() });
		const withEvent = reduceRound(snapshot, { type: 'event', event: anEvent(3_000, 'freeze') });
		const withSeconds = reduceRound(withEvent, { type: 'seconds', seconds: [aSecond(1, 25), aSecond(2)] });
		const withReading = reduceRound(withSeconds, { type: 'auditor', reading: aReading() });
		const finished = reduceRound(withReading, { type: 'finished', replay: aReplay({ verdict: 'pass' }) });

		expect(withSeconds.kind === 'following' && withSeconds.replay.seconds.map((second) => second.ok)).toEqual([20, 25, 20]);
		expect(withSeconds.kind === 'following' && withSeconds.newEvents).toBe(1);
		expect(finished.kind).toBe('finished');
		expect(finished.kind === 'finished' && finished.reading).toEqual(aReading());
	});

	it('starts over on a new snapshot, which is how a reconnection catches up', () => {
		const first = reduceRound(initialRoundState, { type: 'snapshot', replay: aReplay() });
		const withEvent = reduceRound(first, { type: 'event', event: anEvent(3_000, 'freeze') });
		const again = reduceRound(withEvent, { type: 'snapshot', replay: aReplay({ events: [] }) });

		expect(again.kind === 'following' && again.newEvents).toBe(0);
		expect(again.kind === 'following' && again.replay.events).toEqual([]);
	});

	it('ignores updates before the snapshot', () => {
		expect(reduceRound(initialRoundState, { type: 'event', event: anEvent(1, 'kill') })).toBe(initialRoundState);
	});
});

describe('mergeSeconds', () => {
	it('keeps the known seconds when the update is empty', () => {
		const known = [aSecond(0)];

		expect(mergeSeconds(known, [])).toBe(known);
	});
});

describe('phaseOf', () => {
	it('reads the phase from the cure events and the end of the stream', () => {
		expect(phaseOf(aReplay(), false)).toBe('attack');
		expect(phaseOf(aReplay({ events: [anEvent(1, 'heal_start')] }), false)).toBe('heal');
		expect(phaseOf(aReplay({ events: [anEvent(1, 'heal_start'), anEvent(2, 'heal_complete')] }), false)).toBe('check');
		expect(phaseOf(aReplay(), true)).toBe('verdict');
	});
});

describe('guaranteeView', () => {
	it('prefers the checker, follows the auditor for the money and waits for the rest', () => {
		const checks = [{ id: 'I2', status: 'failed' as const, violations: 3 }];

		expect(guaranteeView('I2', checks, null)).toEqual({ kind: 'final', status: 'failed', violations: 3 });
		expect(guaranteeView('I1', [], aReading(0))).toEqual({ kind: 'live', isHolding: true });
		expect(guaranteeView('I1', [], aReading(3_750))).toEqual({ kind: 'live', isHolding: false });
		expect(guaranteeView('I4', [], aReading(0, -1))).toEqual({ kind: 'live', isHolding: false });
		expect(guaranteeView('I5', [], aReading())).toEqual({ kind: 'later' });
	});
});

describe('feedOf', () => {
	it('numbers the attacks in order and lists the newest first', () => {
		const events = [anEvent(1, 'kill'), anEvent(2, 'freeze', 'bank-worker-2'), anEvent(3, 'heal_start')];
		const feed = feedOf(events);

		expect(feed.map((item) => item.label)).toEqual([null, '2', '1']);
		expect(feed[1]?.what).toBe('Congelei o bank-worker-2 por 45s.');
		expect(feed[2]?.expected).toMatch(/^Esperado: /);
	});

	it('groups the same fault in a row into one item, whoever it hit', () => {
		const fired = (tMs: number, target: string): RoundEvent => ({
			...anEvent(tMs, 'failpoint_fired', target),
			detail: 'relay.after_save',
		});
		const events = [
			anEvent(500, 'kill'),
			fired(62_000, 'bank-worker-1'),
			fired(65_000, 'bank-worker-2'),
			fired(66_000, 'bank-worker-1'),
			anEvent(90_000, 'heal_start'),
		];
		const [heal, group, kill] = feedOf(events);

		expect(heal?.label).toBeNull();
		expect(group).toMatchObject({ label: '2–4', count: 3, fromMs: 62_000, toMs: 66_000, code: 'relay.after_save' });
		expect(group?.what).toBe('Falha injetada no meio do código do bank-worker-1 e bank-worker-2.');
		expect(kill?.label).toBe('1');
	});
});

describe('headlineOf', () => {
	it('says what happens now, and how the round ended', () => {
		const attacked = headlineOf({ replay: aReplay(), phase: 'attack', elapsedSeconds: 192, reading: aReading() });
		const passed = headlineOf({
			replay: aReplay({ verdict: 'pass' }),
			phase: 'verdict',
			elapsedSeconds: 0,
			reading: null,
		});
		const broken = aReplay({ verdict: 'fail', checks: [{ id: 'I1', status: 'failed', violations: 2 }] });

		expect(attacked).toBe('O banco está sob ataque há 3 min 12 s, e nenhum centavo sumiu.');
		expect(passed).toBe('Passou: um ataque, nenhum centavo perdido.');
		expect(headlineOf({ replay: broken, phase: 'verdict', elapsedSeconds: 0, reading: null })).toBe('Quebrou: nenhum dinheiro some nem aparece.');
	});
});

describe('expectationOf', () => {
	it('expects a robust bank to pass and a control to break', () => {
		expect(expectationOf('robust')).toMatch(/devem se manter/);
		expect(expectationOf('robust,-fencing')).toBe('Sem fencing, alguma garantia deve quebrar: é o controle que prova que a defesa funciona.');
		expect(expectationOf('')).toMatch(/ainda não foi lido/);
	});
});

describe('spanOf', () => {
	it('covers the planned attack, or more when the cure runs past it', () => {
		expect(spanOf({ durationSeconds: 300, seconds: [aSecond(0)], lastEventSeconds: 0, nowSeconds: null })).toBe(300);
		expect(spanOf({ durationSeconds: 180, seconds: [aSecond(239)], lastEventSeconds: 200, nowSeconds: null })).toBe(240);
	});
});

describe('clusterMarkers', () => {
	it('merges attacks too close to read apart and keeps the rest', () => {
		const markers = clusterMarkers([100, 105, 108, 200, 300, 302], 22);

		expect(markers.map((marker) => marker.label)).toEqual(['1–3', '4', '5–6']);
		expect(clusterMarkers([], 22)).toEqual([]);
	});
});

describe('attack ranges', () => {
	it('reads the shared labels and tells which ranges touch', () => {
		expect(rangeOfLabel('10–14')).toEqual({ from: 10, to: 14 });
		expect(rangeOfLabel('7')).toEqual({ from: 7, to: 7 });
		expect(rangeOfLabel(null)).toBeNull();
		expect(overlaps({ from: 10, to: 14 }, 14, 20)).toBe(true);
		expect(overlaps({ from: 10, to: 14 }, 15, 20)).toBe(false);
		expect(overlaps(null, 1, 2)).toBe(false);
	});
});

describe('defenses in the feed', () => {
	it('tells a defense in its own tone, groups repeats and adds their counts', () => {
		const resumed = (tMs: number, count: number): RoundEvent => ({
			...anEvent(tMs, 'defense', 'bank-worker-2'),
			detail: 'outbox.resumed',
			subject: 'pix_out.submit',
			count,
		});
		const lease = {
			...anEvent(9_000, 'defense', 'bank-worker-2'),
			detail: 'lease.acquired',
			subject: 'reconciler',
			count: 1,
		};
		const down = { ...anEvent(4_000, 'process_state', 'bank-worker-1'), detail: 'down' };
		const feed = feedOf([anEvent(1_000, 'kill', 'bank-worker-1'), down, resumed(5_000, 3), resumed(6_000, 1), lease]);

		expect(feed.map((item) => item.tone)).toEqual(['defense', 'defense', 'attack']);
		expect(feed[0]?.what).toBe('O bank-worker-2 assumiu o lease do conciliador.');
		expect(feed[1]).toMatchObject({ count: 2, what: 'O bank-worker-2 retomou 4 PIX de uma tentativa anterior.' });
	});

	it('tells a defense it does not know as a defense, never as something else', () => {
		const odd = { ...anEvent(1, 'defense', 'bank-api'), detail: 'new.defense' };

		expect(feedOf([odd])[0]?.what).toBe('O banco registrou uma defesa (new.defense).');
	});
});

describe('narrate', () => {
	it('tells the last attack and waits for the defense, then tells the defense', () => {
		const kill = anEvent(10_000, 'kill', 'bank-worker-1');
		const resumed = { ...anEvent(14_000, 'defense', 'bank-worker-2'), detail: 'outbox.resumed', count: 3 };

		const waiting = narrate([kill, resumed], 12_000, 'attack');
		expect(waiting.now).toBe('Derrubei o bank-worker-1.');
		expect(waiting.defense).toMatchObject({ isWaiting: true });

		const defended = narrate([kill, resumed], 15_000, 'attack');
		expect(defended.defense).toEqual({
			text: 'O bank-worker-2 retomou 3 PIX de uma tentativa anterior.',
			isWaiting: false,
		});
	});

	it('falls back to the phase once the attack is old', () => {
		expect(narrate([anEvent(1_000, 'kill')], 60_000, 'heal')).toEqual({
			now: 'Os ataques pararam: o banco termina o que ficou pendente.',
			defense: null,
		});
	});
});
