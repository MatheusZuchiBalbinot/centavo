import { describe, expect, it } from 'vitest';

import type { RoundEvent } from '../api/replay';
import { anEvent } from '../test/fixtures';
import { systemStateAt } from './systemState';
import { linksOf, nodesOf } from './topology';

function event(tMs: number, kind: RoundEvent['kind'], fields: Partial<RoundEvent> = {}): RoundEvent {
	return { ...anEvent(tMs, kind, ''), ...fields };
}

describe('topology', () => {
	it('reads the nodes and links the events name', () => {
		expect(nodesOf('bank-worker-1, bank-worker-2')).toEqual(['worker1', 'worker2']);
		expect(nodesOf('toxiproxy')).toEqual([]);
		expect(linksOf('api_pg, worker1_pg, nothing')).toEqual(['api_pg', 'worker1_pg']);
	});
});

describe('systemStateAt', () => {
	it('follows a process down and back up as the lab reports it', () => {
		const events = [
			event(1_000, 'kill', { target: 'bank-worker-1' }),
			event(2_000, 'process_state', { target: 'bank-worker-1', detail: 'starting' }),
			event(9_000, 'process_state', { target: 'bank-worker-1', detail: 'up' }),
		];

		expect(systemStateAt(events, 1_500).nodes.worker1).toMatchObject({ status: 'down', isHit: true });
		expect(systemStateAt(events, 5_000).nodes.worker1).toMatchObject({ status: 'starting', isHit: false });
		expect(systemStateAt(events, 10_000).nodes.worker1.status).toBe('up');
	});

	it('brings every process back up when the cure completes, as the lab stops watching there', () => {
		const events = [
			event(1_000, 'process_state', { target: 'bank-api', detail: 'starting' }),
			event(2_000, 'heal_start'),
			event(7_000, 'heal_complete'),
		];

		expect(systemStateAt(events, 5_000).nodes.api.status).toBe('starting');
		expect(systemStateAt(events, 8_000).nodes.api.status).toBe('up');
	});

	it('keeps a frozen process frozen for the length of the freeze', () => {
		const events = [event(10_000, 'freeze', { target: 'bank-worker-2', duration: '45s' })];

		expect(systemStateAt(events, 30_000).nodes.worker2).toMatchObject({ status: 'frozen', frozenUntilMs: 55_000 });
		expect(systemStateAt(events, 60_000).nodes.worker2.status).toBe('up');
	});

	it('slows or cuts the named links until the fault ends or the cure starts', () => {
		const events = [
			event(1_000, 'network', { target: 'api_pg, worker1_pg', detail: 'latency' }),
			event(2_000, 'network', { target: 'worker2_partner', detail: 'timeout' }),
			event(5_000, 'network', { target: 'api_pg, worker1_pg', detail: 'latency', subject: 'end' }),
			event(8_000, 'heal_start'),
		];

		const during = systemStateAt(events, 3_000);
		expect(during.links.api_pg).toEqual({ status: 'slow', toxic: 'latency' });
		expect(during.links.worker2_partner.status).toBe('cut');
		expect(systemStateAt(events, 6_000).links.api_pg.status).toBe('ok');
		expect(systemStateAt(events, 6_000).links.worker2_partner.status).toBe('cut');

		const healed = systemStateAt(events, 9_000);
		expect(healed.links.worker2_partner.status).toBe('ok');
		expect(healed.isHealing).toBe(true);
	});

	it('times how long a process has been out and shows the defense it just made', () => {
		const events = [
			event(1_000, 'kill', { target: 'bank-worker-1' }),
			event(2_000, 'process_state', { target: 'bank-worker-1', detail: 'down' }),
			event(4_000, 'process_state', { target: 'bank-worker-1', detail: 'starting' }),
			event(5_000, 'defense', { target: 'bank-worker-2', detail: 'lease.acquired', subject: 'reconciler' }),
		];

		const state = systemStateAt(events, 6_000);
		expect(state.nodes.worker1).toMatchObject({ status: 'starting', statusSinceMs: 4_000 });
		expect(state.nodes.worker2.reaction?.detail).toBe('lease.acquired');
		expect(systemStateAt(events, 3_000).nodes.worker1.statusSinceMs).toBe(1_000);
		expect(systemStateAt(events, 10_000).nodes.worker2.reaction).toBeNull();
	});

	it('cuts a link under a partition', () => {
		const events = [event(1_000, 'network', { target: 'worker1_partner', detail: 'blackhole', isManual: true })];

		expect(systemStateAt(events, 2_000).links.worker1_partner.status).toBe('cut');
	});

	it('lights the defending node, unmasks the partner at the cure and moves the leader on failover', () => {
		const events = [
			event(1_000, 'arm_partner_modes', { detail: 'double_process' }),
			event(2_000, 'defense', { target: 'bank-worker-2', detail: 'outbox.resumed', count: 3 }),
			event(3_000, 'failover'),
		];

		const state = systemStateAt(events, 3_500);
		expect(state.nodes.partner.isLying).toBe(true);
		expect(state.nodes.worker2.isDefending).toBe(true);
		expect(state.nodes.postgres.isLeader).toBe(false);
		expect(state.nodes.replica.isLeader).toBe(true);

		const cured = systemStateAt([...events, event(4_000, 'heal_start')], 9_000);
		expect(cured.nodes.partner.isLying).toBe(false);
		expect(cured.nodes.worker2.isDefending).toBe(false);
	});
});
