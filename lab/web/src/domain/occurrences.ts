import type { RoundEvent, RoundTransfer } from '../api/replay';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';
import { formatClock } from './duration';
import { describeEvent } from './feed';
import { formatCents } from './money';
import { isAttackEvent, isDefenseEvent } from './phase';

// Each kind has its own color on the strip.
export type AttackKind = 'kill' | 'freeze' | 'network' | 'failpoint' | 'database' | 'clock' | 'partner' | 'other';

export type MoneyKind = 'transfer' | 'pix';

export type OccurrenceKind = AttackKind | 'defense' | MoneyKind;

// The rows of the strip, top to bottom: the chaos, the bank's answers and
// the clients' money.
export type OccurrenceRow = 'attack' | 'defense' | 'money';

export type Occurrence =
	| {
			readonly row: 'attack';
			readonly key: string;
			readonly kind: AttackKind;
			readonly tMs: number;
			readonly number: number;
			readonly event: RoundEvent;
	  }
	| { readonly row: 'defense'; readonly key: string; readonly kind: 'defense'; readonly tMs: number; readonly event: RoundEvent }
	| { readonly row: 'money'; readonly key: string; readonly kind: MoneyKind; readonly tMs: number; readonly transfer: RoundTransfer };

export type OccurrenceDetails = {
	readonly title: string;
	readonly text: string;
	readonly fields: readonly { readonly label: string; readonly value: string }[];
};

export const OCCURRENCE_ROWS: readonly OccurrenceRow[] = ['attack', 'defense', 'money'];

const MS_PER_SECOND = 1_000;
// The tail of an account id: the head of a UUIDv7 is its time, shared by
// every account the round created.
const ACCOUNT_TAIL = 6;

const subjects: Readonly<Partial<Record<string, string>>> = messages.feed.subjects;

const ATTACK_KINDS: Readonly<Partial<Record<RoundEvent['kind'], AttackKind>>> = {
	kill: 'kill',
	freeze: 'freeze',
	network: 'network',
	failpoint_fired: 'failpoint',
	database: 'database',
	failover: 'database',
	skew_clock: 'clock',
	arm_partner_modes: 'partner',
};

export function attackKindOf(event: RoundEvent): AttackKind {
	return ATTACK_KINDS[event.kind] ?? 'other';
}

// In time order; attacks keep the numbers the log and the feed give them.
export function occurrencesOf(events: readonly RoundEvent[], transfers: readonly RoundTransfer[]): readonly Occurrence[] {
	const occurrences: Occurrence[] = [];
	let attacks = 0;

	for (const [index, event] of events.entries()) {
		if (isAttackEvent(event)) {
			attacks += 1;
			occurrences.push({ row: 'attack', key: `e${index}`, kind: attackKindOf(event), tMs: event.tMs, number: attacks, event });
			continue;
		}

		if (isDefenseEvent(event)) {
			occurrences.push({ row: 'defense', key: `e${index}`, kind: 'defense', tMs: event.tMs, event });
		}
	}

	for (const transfer of transfers) {
		occurrences.push({ row: 'money', key: transfer.op, kind: transfer.kind === 'pix_out' ? 'pix' : 'transfer', tMs: transfer.tMs, transfer });
	}

	return occurrences.sort(byTime);
}

export function isManualOccurrence(occurrence: Occurrence): boolean {
	return occurrence.row === 'attack' && occurrence.event.isManual;
}

// A transfer that was not done: refused, or left without an answer.
export function isUnsettled(occurrence: Occurrence): boolean {
	return occurrence.row === 'money' && occurrence.transfer.outcome !== 'ok';
}

export function detailsOf(occurrence: Occurrence): OccurrenceDetails {
	const copy = messages.arena.occurrences;
	const at = { label: copy.fields.at, value: formatClock(occurrence.tMs / MS_PER_SECOND) };

	switch (occurrence.row) {
		case 'attack':
			return attackDetails(occurrence.event, occurrence.number, at);
		case 'defense':
			return defenseDetails(occurrence.event, at);
		case 'money':
			return moneyDetails(occurrence.transfer, at);
	}
}

type Field = OccurrenceDetails['fields'][number];

function attackDetails(event: RoundEvent, number: number, at: Field): OccurrenceDetails {
	const copy = messages.arena.occurrences;
	const kind = copy.kinds[attackKindOf(event)];
	const fields = [
		at,
		{ label: copy.fields.target, value: event.target },
		{ label: copy.fields.detail, value: event.detail },
		{ label: copy.fields.duration, value: event.duration },
		{ label: copy.fields.origin, value: event.isManual ? copy.origin.manual : copy.origin.scenario },
	].filter(hasValue);

	return { title: format(copy.attackTitle, { number, kind }), text: describeEvent(event).what, fields };
}

function defenseDetails(event: RoundEvent, at: Field): OccurrenceDetails {
	const copy = messages.arena.occurrences;
	const fields = [
		at,
		{ label: copy.fields.who, value: event.target },
		{ label: copy.fields.subject, value: subjects[event.subject] ?? event.subject },
		{ label: copy.fields.count, value: event.count > 0 ? String(event.count) : '' },
	].filter(hasValue);

	return { title: copy.defenseTitle, text: describeEvent(event).what, fields };
}

function moneyDetails(transfer: RoundTransfer, at: Field): OccurrenceDetails {
	const copy = messages.arena.occurrences;
	const destination = transfer.kind === 'pix_out' ? format(copy.pixKey, { key: transfer.destination }) : accountOf(transfer.to);
	const fields = [
		at,
		{ label: copy.fields.result, value: resultOf(transfer) },
		{ label: copy.fields.latency, value: format(copy.latency, { ms: Math.round(transfer.latencyMs) }) },
		{ label: copy.fields.op, value: transfer.op },
	];
	const text = format(copy.moneyText, { amount: formatCents(transfer.amountCents), from: accountOf(transfer.from), to: destination });

	return { title: transfer.kind === 'pix_out' ? copy.kinds.pix : copy.kinds.transfer, text, fields };
}

function resultOf(transfer: RoundTransfer): string {
	const results = messages.arena.occurrences.results;

	if (transfer.outcome === 'ok') {
		return results.ok;
	}

	const result = transfer.outcome === 'fail' ? results.fail : results.info;

	return transfer.error === '' ? result : `${result} (${transfer.error})`;
}

function accountOf(id: string): string {
	return format(messages.arena.occurrences.account, { tail: id.slice(-ACCOUNT_TAIL) });
}

function hasValue(field: Field): boolean {
	return field.value !== '';
}

function byTime(a: Occurrence, b: Occurrence): number {
	return a.tMs - b.tMs;
}

// Where the keyboard or the pointer stands on the strip.
export type OccurrenceCursor = { readonly row: OccurrenceRow; readonly index: number };

export type OccurrenceRows = Readonly<Record<OccurrenceRow, readonly Occurrence[]>>;

export function rowsOf(occurrences: readonly Occurrence[]): OccurrenceRows {
	return {
		attack: occurrences.filter((occurrence) => occurrence.row === 'attack'),
		defense: occurrences.filter((occurrence) => occurrence.row === 'defense'),
		money: occurrences.filter((occurrence) => occurrence.row === 'money'),
	};
}

// The occurrence closest in time; -1 for an empty row.
export function nearestIn(row: readonly Occurrence[], tMs: number): number {
	let best = -1;

	for (const [index, occurrence] of row.entries()) {
		const isCloser = best === -1 || Math.abs(occurrence.tMs - tMs) < Math.abs((row[best]?.tMs ?? 0) - tMs);

		if (isCloser) {
			best = index;
		}
	}

	return best;
}

// The keyboard starts on the latest attack, or on the latest of anything.
export function firstCursor(rows: OccurrenceRows, nowMs: number): OccurrenceCursor | null {
	for (const row of OCCURRENCE_ROWS) {
		const index = nearestIn(rows[row], nowMs);

		if (index !== -1) {
			return { row, index };
		}
	}

	return null;
}

// Left and right walk a row, up and down change rows at the same moment,
// Home and End jump to a row's ends. Any other key moves nothing.
export function movedCursor(rows: OccurrenceRows, cursor: OccurrenceCursor, key: string): OccurrenceCursor | null {
	const row = rows[cursor.row];

	switch (key) {
		case 'ArrowRight':
			return { ...cursor, index: Math.min(row.length - 1, cursor.index + 1) };
		case 'ArrowLeft':
			return { ...cursor, index: Math.max(0, cursor.index - 1) };
		case 'Home':
			return { ...cursor, index: 0 };
		case 'End':
			return { ...cursor, index: row.length - 1 };
		case 'ArrowDown':
			return otherRow(rows, cursor, 1);
		case 'ArrowUp':
			return otherRow(rows, cursor, -1);
		default:
			return null;
	}
}

function otherRow(rows: OccurrenceRows, cursor: OccurrenceCursor, step: number): OccurrenceCursor {
	const tMs = rows[cursor.row][cursor.index]?.tMs ?? 0;

	for (let position = OCCURRENCE_ROWS.indexOf(cursor.row) + step; position >= 0 && position < OCCURRENCE_ROWS.length; position += step) {
		const row = OCCURRENCE_ROWS[position];
		const index = row === undefined ? -1 : nearestIn(rows[row], tMs);
		const hasOccurrence = row !== undefined && index !== -1;

		if (hasOccurrence) {
			return { row, index };
		}
	}

	return cursor;
}
