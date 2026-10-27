import { describe, expect, it } from 'vitest';

import type { RoundEvent, RoundTransfer } from '../api/replay';
import { anEvent } from '../test/fixtures';
import { formatCents } from './money';
import { detailsOf, firstCursor, movedCursor, occurrencesOf, rowsOf } from './occurrences';

function event(tMs: number, kind: RoundEvent['kind'], fields: Partial<RoundEvent> = {}): RoundEvent {
	return { ...anEvent(tMs, kind, ''), ...fields };
}

function aTransfer(tMs: number, fields: Partial<RoundTransfer> = {}): RoundTransfer {
	return {
		tMs,
		kind: 'transfer',
		outcome: 'ok',
		op: `r0001-p01-${tMs}`,
		from: '01a17ee4-ae80-71b2-8f46-95c0f07d389a',
		to: '01a17ee4-ad7a-7e6a-adf2-1b4d5170cfeb',
		destination: '',
		amountCents: 5_238,
		latencyMs: 29.4,
		error: '',
		...fields,
	};
}

describe('occurrencesOf', () => {
	const events = [
		event(1_000, 'kill', { target: 'bank-worker-1', isManual: true }),
		event(2_000, 'process_state', { target: 'bank-worker-1', detail: 'down' }),
		event(3_000, 'defense', { target: 'bank-worker-2', detail: 'lease.acquired', subject: 'reconciler' }),
		event(4_000, 'network', { target: 'api_pg', detail: 'latency' }),
	];
	const transfers = [aTransfer(1_500), aTransfer(2_500, { kind: 'pix_out', outcome: 'fail', destination: 'dest-7594', error: 'INSUFFICIENT_FUNDS' })];

	it('puts attacks, defenses and money in time order, each with its kind, and leaves process states out', () => {
		const occurrences = occurrencesOf(events, transfers);

		expect(occurrences.map((occurrence) => occurrence.kind)).toEqual(['kill', 'transfer', 'pix', 'defense', 'network']);
		expect(occurrences.filter((occurrence) => occurrence.row === 'attack').map((occurrence) => occurrence.number)).toEqual([1, 2]);
	});

	it('describes each dot with what it was', () => {
		const [kill, transfer, pix, defense] = occurrencesOf(events, transfers);

		if (kill === undefined || transfer === undefined || pix === undefined || defense === undefined) {
			throw new Error('the round lost an occurrence');
		}

		expect(detailsOf(kill)).toMatchObject({ title: 'Ataque 1 · queda', text: 'O bank-worker-1 caiu.' });
		expect(detailsOf(kill).fields).toContainEqual({ label: 'Origem', value: 'ataque manual' });
		expect(detailsOf(transfer)).toMatchObject({ title: 'transferência', text: `${formatCents(5_238)} de conta …7d389a para conta …70cfeb.` });
		expect(detailsOf(transfer).fields).toContainEqual({ label: 'Tempo de resposta', value: '29 ms' });
		expect(detailsOf(pix).text).toBe(`${formatCents(5_238)} de conta …7d389a para a chave dest-7594.`);
		expect(detailsOf(pix).fields).toContainEqual({ label: 'Resultado', value: 'recusada (INSUFFICIENT_FUNDS)' });
		expect(detailsOf(defense).fields).toContainEqual({ label: 'Protegeu', value: 'conciliador' });
	});

	it('walks a row with the arrows and changes rows at the same moment', () => {
		const rows = rowsOf(occurrencesOf(events, transfers));
		const start = firstCursor(rows, 4_500);

		expect(start).toEqual({ row: 'attack', index: 1 });

		if (start === null) {
			throw new Error('no cursor');
		}

		expect(movedCursor(rows, start, 'ArrowLeft')).toEqual({ row: 'attack', index: 0 });
		expect(movedCursor(rows, start, 'ArrowDown')).toEqual({ row: 'defense', index: 0 });
		expect(movedCursor(rows, { row: 'attack', index: 0 }, 'ArrowDown')).toEqual({ row: 'defense', index: 0 });
		expect(movedCursor(rows, { row: 'defense', index: 0 }, 'ArrowDown')).toEqual({ row: 'money', index: 1 });
		expect(movedCursor(rows, start, 'Enter')).toBeNull();
	});
});
