import { describe, expect, it } from 'vitest';

import type { RoundEvent } from '../api/replay';
import { anEvent } from '../test/fixtures';
import { incidentsOf, meanRecoveryMs } from './incidents';

function event(tMs: number, kind: RoundEvent['kind'], fields: Partial<RoundEvent> = {}): RoundEvent {
	return { ...anEvent(tMs, kind, ''), ...fields };
}

describe('incidentsOf', () => {
	it('pairs a kill with the defense that answered it and times the way back', () => {
		const events = [
			event(10_000, 'kill', { target: 'bank-worker-1' }),
			event(11_000, 'process_state', { target: 'bank-worker-1', detail: 'down' }),
			event(13_200, 'defense', { target: 'bank-worker-2', detail: 'lease.acquired', subject: 'reconciler' }),
			event(15_000, 'process_state', { target: 'bank-worker-1', detail: 'starting' }),
			event(24_000, 'process_state', { target: 'bank-worker-1', detail: 'up' }),
		];

		const during = incidentsOf(events, 16_000)[0];
		expect(during).toMatchObject({ label: '1', atMs: 10_000, isManual: false, recovery: { kind: 'out' } });
		expect(during?.response).toEqual({ kind: 'answered', what: 'O bank-worker-2 assumiu o lease do conciliador.', afterMs: 3_200 });

		expect(incidentsOf(events, 30_000)[0]?.recovery).toEqual({ kind: 'back', afterMs: 14_000 });
	});

	it('gives a defense to the attack right before it, newest attack first', () => {
		const events = [
			event(1_000, 'kill', { target: 'bank-api' }),
			event(5_000, 'freeze', { target: 'bank-worker-2', duration: '20s', isManual: true }),
			event(6_000, 'defense', { target: 'bank-worker-1', detail: 'outbox.resumed', count: 2 }),
		];

		const [freeze, kill] = incidentsOf(events, 7_000);

		expect(freeze).toMatchObject({ label: '2', kind: 'freeze', isManual: true, what: 'O bank-worker-2 congelou por 20s.' });
		expect(kill?.kind).toBe('kill');
		expect(freeze?.response).toMatchObject({ kind: 'answered', afterMs: 1_000 });
		expect(kill?.response).toEqual({ kind: 'none' });
		expect(incidentsOf(events, 5_500)[0]?.response).toEqual({ kind: 'waiting' });
	});

	it('calls a fault absorbed when the lab never saw the process go out', () => {
		const events = [event(1_000, 'failpoint_fired', { target: 'bank-api', detail: 'transfer.after_debit' })];

		expect(incidentsOf(events, 3_000)[0]?.recovery.kind).toBe('out');
		expect(incidentsOf(events, 9_000)[0]?.recovery.kind).toBe('absorbed');
	});

	it('times a link from its fault to its end, or to the cure', () => {
		const events = [
			event(1_000, 'network', { target: 'api_pg', detail: 'latency' }),
			event(2_000, 'network', { target: 'worker1_partner', detail: 'blackhole' }),
			event(9_000, 'network', { target: 'api_pg', detail: 'latency', subject: 'end' }),
			event(30_000, 'heal_start'),
		];

		const [cut, slow] = incidentsOf(events, 31_000);

		expect(slow?.recovery).toEqual({ kind: 'back', afterMs: 8_000 });
		expect(cut?.recovery).toEqual({ kind: 'back', afterMs: 28_000 });
		expect(incidentsOf(events, 10_000)[0]?.recovery.kind).toBe('out');
	});

	it('leaves the clock and the lying partner to the cure, and averages what came back', () => {
		const events = [
			event(1_000, 'skew_clock', { target: 'bank-worker-1', detail: '+90s' }),
			event(2_000, 'kill', { target: 'partner' }),
			event(3_000, 'process_state', { target: 'partner', detail: 'down' }),
			event(8_000, 'process_state', { target: 'partner', detail: 'up' }),
			event(9_000, 'kill', { target: 'bank-api' }),
			event(10_000, 'process_state', { target: 'bank-api', detail: 'starting' }),
			event(25_000, 'process_state', { target: 'bank-api', detail: 'up' }),
		];

		const incidents = incidentsOf(events, 26_000);

		expect(incidents.at(-1)?.recovery.kind).toBe('until_cure');
		expect(meanRecoveryMs(incidents)).toBe(11_000);
		expect(meanRecoveryMs(incidentsOf(events, 1_500))).toBeNull();
	});
});
