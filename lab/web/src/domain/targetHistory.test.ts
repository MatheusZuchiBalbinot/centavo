import { describe, expect, it } from 'vitest';

import type { RoundEvent } from '../api/replay';
import { anEvent } from '../test/fixtures';
import { historyOf } from './targetHistory';

function event(tMs: number, kind: RoundEvent['kind'], fields: Partial<RoundEvent> = {}): RoundEvent {
	return { ...anEvent(tMs, kind, ''), ...fields };
}

describe('historyOf', () => {
	const events = [
		event(1_000, 'kill', { target: 'bank-worker-1', isManual: true }),
		event(2_000, 'process_state', { target: 'bank-worker-1', detail: 'starting' }),
		event(3_000, 'defense', { target: 'bank-worker-2', detail: 'lease.acquired', subject: 'reconciler' }),
		event(4_000, 'network', { target: 'api_pg, worker1_pg', detail: 'latency' }),
		event(9_000, 'network', { target: 'api_pg, worker1_pg', detail: 'latency', subject: 'end' }),
	];

	it('tells what happened to one process, newest first, in plain words', () => {
		expect(historyOf({ kind: 'node', id: 'worker1' }, events)).toEqual([
			{ key: '2000-1', tMs: 2_000, text: 'Ficou reiniciando.', tone: 'state' },
			{ key: '1000-0', tMs: 1_000, text: 'O bank-worker-1 caiu.', tone: 'attack' },
		]);
		expect(historyOf({ kind: 'node', id: 'worker2' }, events)).toMatchObject([{ tone: 'defense' }]);
	});

	it('follows a link through its fault and its end', () => {
		expect(historyOf({ kind: 'link', id: 'worker1_pg' }, events).map((line) => line.text)).toEqual([
			'A rede voltou ao normal.',
			'A rede começou a falhar em api_pg, worker1_pg.',
		]);
		expect(historyOf({ kind: 'link', id: 'client_api' }, events)).toEqual([]);
	});
});
