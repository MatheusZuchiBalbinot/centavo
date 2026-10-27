import type { RoundEvent } from '../api/replay';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';
import { isAttackEvent, isDefenseEvent, isStateEvent } from './phase';

export type FeedTone = 'attack' | 'defense' | 'heal' | 'other';

// A feed item says what happened and what should follow from it. Attacks of
// the same kind in a row are one item: a burst of the same failpoint reads
// as one line with a count, not as a wall of identical lines.
export type FeedItem = {
	readonly key: string;
	readonly fromMs: number;
	readonly toMs: number;
	readonly tone: FeedTone;
	// label matches the markers on the chart: "7", or "10–14" for a group;
	// cure events have none.
	readonly label: string | null;
	readonly count: number;
	readonly what: string;
	readonly expected: string;
	// code is the fault's technical name (a failpoint, a toxic), shown apart
	// so the sentence stays readable.
	readonly code: string;
	// raw are the events behind the item, for the technical mode.
	readonly raw: readonly RoundEvent[];
};

type NumberedEvent = { readonly event: RoundEvent; readonly number: number | null; readonly index: number };

// More targets than this are counted instead of listed.
const MAX_LISTED_TARGETS = 3;

// The kinds whose detail is a code name rather than words.
const CODE_KINDS: ReadonlySet<RoundEvent['kind']> = new Set(['failpoint_fired', 'network', 'arm_partner_modes', 'database']);

// feedOf tells every event of the round, newest first, numbering the attacks
// in the order they happened and grouping repeats.
export function feedOf(events: readonly RoundEvent[]): readonly FeedItem[] {
	// Process states draw the map; told in the feed they would be three lines
	// for every kill.
	return groupRuns(numbered(events)).filter(isTold).map(itemOf).reverse();
}

// describeEvent is the sentence of one event, also used by its toast.
export function describeEvent(event: RoundEvent): { what: string; expected: string } {
	return describe(event, event.target || messages.feed.someone, event.count);
}

function numbered(events: readonly RoundEvent[]): readonly NumberedEvent[] {
	let attackNumber = 0;

	return events.map((event, index) => {
		const isAttack = isAttackEvent(event);

		if (isAttack) {
			attackNumber += 1;
		}

		return { event, number: isAttack ? attackNumber : null, index };
	});
}

// groupRuns puts consecutive attacks of the same kind and fault together;
// cure events always stand alone.
function groupRuns(events: readonly NumberedEvent[]): readonly (readonly NumberedEvent[])[] {
	const runs: NumberedEvent[][] = [];

	for (const current of events) {
		const run = runs.at(-1);
		const previous = run?.at(-1);
		const isSameAttack = previous !== undefined && isRepeat(previous, current);

		if (run && isSameAttack) {
			run.push(current);
			continue;
		}

		runs.push([current]);
	}

	return runs;
}

// isRepeat groups attacks of the same fault, and defenses of the same kind;
// the cure and the process states always stand alone.
function isRepeat(previous: NumberedEvent, current: NumberedEvent): boolean {
	const areAttacks = previous.number !== null && current.number !== null;
	const areDefenses = isDefenseEvent(previous.event) && isDefenseEvent(current.event);
	const isSameFault = previous.event.kind === current.event.kind && previous.event.detail === current.event.detail;

	return (areAttacks || areDefenses) && isSameFault;
}

function itemOf(run: readonly NumberedEvent[]): FeedItem {
	const first = run[0];
	const last = run.at(-1);

	if (first === undefined || last === undefined) {
		throw new Error('a feed run is never empty');
	}

	const total = run.reduce(addCount, 0);
	const { what, expected } = describe(first.event, targetsOf(run), total);

	return {
		key: `${first.event.tMs}-${first.index}`,
		fromMs: first.event.tMs,
		toMs: last.event.tMs,
		tone: toneOf(first.event),
		label: labelOf(first.number, last.number),
		count: run.length,
		what,
		expected,
		code: CODE_KINDS.has(first.event.kind) ? first.event.detail : '',
		raw: run.map((item) => item.event),
	};
}

function describe(event: RoundEvent, target: string, count: number): { what: string; expected: string } {
	const copy = copyOf(event);
	const values = { target, detail: event.detail, duration: event.duration, count, subject: subjectOf(event.subject) };
	const expected = copy.expect === '' ? '' : format(messages.feed.expected, { text: copy.expect });

	return { what: format(copy.what, values), expected };
}

const defenseCopy: Readonly<Partial<Record<string, { what: string; expect: string }>>> = messages.feed.defenses;
const subjects: Readonly<Partial<Record<string, string>>> = messages.feed.subjects;

// copyOf picks the sentence of an event; a defense the panel does not know
// yet is told as one, never as something else.
function copyOf(event: RoundEvent): { what: string; expect: string } {
	if (event.kind !== 'defense') {
		return messages.feed.events[event.kind];
	}

	return defenseCopy[event.detail] ?? messages.feed.unknownDefense;
}

function subjectOf(subject: string): string {
	return subjects[subject] ?? subject;
}

// targetsOf names who was hit: "bank-worker-1 e bank-worker-2", or a count
// when the list would be long.
function targetsOf(run: readonly NumberedEvent[]): string {
	const targets = [...new Set(run.map((item) => item.event.target).filter(Boolean))];

	if (targets.length === 0) {
		return messages.feed.someone;
	}

	if (targets.length > MAX_LISTED_TARGETS) {
		return format(messages.feed.manyTargets, { count: targets.length });
	}

	return new Intl.ListFormat('pt-BR', { type: 'conjunction' }).format(targets);
}

function isTold(run: readonly NumberedEvent[]): boolean {
	return !run.every(isStateItem);
}

function isStateItem(item: NumberedEvent): boolean {
	return isStateEvent(item.event);
}

function addCount(total: number, item: NumberedEvent): number {
	return total + item.event.count;
}

function labelOf(first: number | null, last: number | null): string | null {
	if (first === null || last === null) {
		return null;
	}

	return first === last ? String(first) : `${first}–${last}`;
}

function toneOf(event: RoundEvent): FeedTone {
	if (event.kind === 'other') {
		return 'other';
	}

	if (isDefenseEvent(event)) {
		return 'defense';
	}

	return isAttackEvent(event) ? 'attack' : 'heal';
}
