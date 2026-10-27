import type { RoundEvent } from '../api/replay';
import { describeEvent } from './feed';
import { attackKindOf, type AttackKind } from './occurrences';
import { isAttackEvent, isDefenseEvent, isHealStart, isNetworkEnd } from './phase';

// How the bank got back from one attack: the time it took, or why there is
// nothing to time.
export type Recovery =
	{ readonly kind: 'back'; readonly afterMs: number } | { readonly kind: 'out' } | { readonly kind: 'until_cure' } | { readonly kind: 'absorbed' };

// The first defense after the attack, or whether one can still come.
export type Response =
	{ readonly kind: 'answered'; readonly what: string; readonly afterMs: number } | { readonly kind: 'waiting' } | { readonly kind: 'none' };

export type Incident = {
	readonly key: string;
	readonly label: string;
	// The color the strip gives the same attack.
	readonly kind: AttackKind;
	readonly atMs: number;
	readonly what: string;
	readonly isManual: boolean;
	readonly response: Response;
	readonly recovery: Recovery;
};

// A defense answers the attack before it, unless it comes this late.
const RESPONSE_WINDOW_MS = 60_000;
// The lab looks at the processes once a second; an outage it did not see
// within this time did not happen.
const OUTAGE_GRACE_MS = 5_000;

const UNTIL_CURE: ReadonlySet<RoundEvent['kind']> = new Set(['skew_clock', 'arm_partner_modes']);
const WITHOUT_OUTAGE: ReadonlySet<RoundEvent['kind']> = new Set(['failover', 'other']);

// Newest first, as the arena lists them.
export function incidentsOf(events: readonly RoundEvent[], nowMs: number): readonly Incident[] {
	const seen = events.filter((event) => event.tMs <= nowMs);
	const runs = runsOf(seen.filter(isAttackEvent));

	function toIncident(run: AttackRun, index: number): Incident {
		const nextAtMs = runs[index + 1]?.first.tMs ?? null;
		const input: IncidentInput = { run, nextAtMs, seen, nowMs };

		return incidentOf(input);
	}

	return runs.map(toIncident).reverse();
}

export function meanRecoveryMs(incidents: readonly Incident[]): number | null {
	const times = incidents.flatMap((incident) => (incident.recovery.kind === 'back' ? [incident.recovery.afterMs] : []));

	if (times.length === 0) {
		return null;
	}

	return times.reduce((total, time) => total + time, 0) / times.length;
}

// One attack, or a burst of the same fault on the same target. Numbered as
// the timeline numbers the attacks.
type AttackRun = { readonly first: RoundEvent; readonly firstNumber: number; readonly lastNumber: number };

type IncidentInput = {
	readonly run: AttackRun;
	readonly nextAtMs: number | null;
	readonly seen: readonly RoundEvent[];
	readonly nowMs: number;
};

function runsOf(attacks: readonly RoundEvent[]): readonly AttackRun[] {
	const runs: AttackRun[] = [];

	for (const [index, attack] of attacks.entries()) {
		const number = index + 1;
		const last = runs.at(-1);
		const isSameRun = last !== undefined && isSameFault(last.first, attack);

		if (isSameRun) {
			runs[runs.length - 1] = { ...last, lastNumber: number };
			continue;
		}

		runs.push({ first: attack, firstNumber: number, lastNumber: number });
	}

	return runs;
}

function isSameFault(previous: RoundEvent, current: RoundEvent): boolean {
	return (
		previous.kind === current.kind &&
		previous.detail === current.detail &&
		previous.target === current.target &&
		previous.isManual === current.isManual
	);
}

function incidentOf({ run, nextAtMs, seen, nowMs }: IncidentInput): Incident {
	const { first } = run;
	const windowEndMs = Math.min(nextAtMs ?? Infinity, first.tMs + RESPONSE_WINDOW_MS);
	const defense = seen.find(isDefenseBetween(first.tMs, windowEndMs)) ?? null;
	const response = responseOf({ attack: first, defense, isOpen: nowMs < windowEndMs });

	return {
		key: `${first.tMs}-${run.firstNumber}`,
		label: labelOf(run),
		kind: attackKindOf(first),
		atMs: first.tMs,
		what: describeEvent(first).what,
		isManual: first.isManual,
		response,
		recovery: recoveryOf(first, seen, nowMs),
	};
}

type ResponseInput = { readonly attack: RoundEvent; readonly defense: RoundEvent | null; readonly isOpen: boolean };

function responseOf({ attack, defense, isOpen }: ResponseInput): Response {
	if (defense !== null) {
		return { kind: 'answered', what: describeEvent(defense).what, afterMs: defense.tMs - attack.tMs };
	}

	return isOpen ? { kind: 'waiting' } : { kind: 'none' };
}

function labelOf(run: AttackRun): string {
	return run.firstNumber === run.lastNumber ? String(run.firstNumber) : `${run.firstNumber}–${run.lastNumber}`;
}

function isDefenseBetween(fromMs: number, toMs: number): (event: RoundEvent) => boolean {
	return (event) => isDefenseEvent(event) && event.tMs >= fromMs && event.tMs < toMs;
}

function recoveryOf(attack: RoundEvent, seen: readonly RoundEvent[], nowMs: number): Recovery {
	if (UNTIL_CURE.has(attack.kind)) {
		return { kind: 'until_cure' };
	}

	if (WITHOUT_OUTAGE.has(attack.kind)) {
		return { kind: 'absorbed' };
	}

	if (attack.kind === 'network') {
		return linkRecoveryOf(attack, seen);
	}

	return outageRecoveryOf(attack, seen, nowMs);
}

// A link is back when its fault ends, or at the latest when the cure starts.
function linkRecoveryOf(attack: RoundEvent, seen: readonly RoundEvent[]): Recovery {
	const end = seen.find((event) => event.tMs >= attack.tMs && isEndOf(attack, event)) ?? null;

	if (end === null) {
		return { kind: 'out' };
	}

	return { kind: 'back', afterMs: end.tMs - attack.tMs };
}

function isEndOf(attack: RoundEvent, event: RoundEvent): boolean {
	const isSameLink = isNetworkEnd(event) && event.target === attack.target;

	return isSameLink || isHealStart(event);
}

// Processes are timed from the lab's own reports: the first time it saw the
// target out after the attack, then the first time it saw it up again.
function outageRecoveryOf(attack: RoundEvent, seen: readonly RoundEvent[], nowMs: number): Recovery {
	const target = firstNameOf(attack.target);
	const states = seen.filter(isStateOfSince(target, attack.tMs));
	const outage = states.find((event) => event.detail !== 'up' && event.tMs - attack.tMs <= OUTAGE_GRACE_MS) ?? null;

	if (outage === null) {
		const isStillWatching = nowMs - attack.tMs < OUTAGE_GRACE_MS;

		return isStillWatching ? { kind: 'out' } : { kind: 'absorbed' };
	}

	const back = states.find((event) => event.detail === 'up' && event.tMs > outage.tMs) ?? null;

	if (back === null) {
		return { kind: 'out' };
	}

	return { kind: 'back', afterMs: back.tMs - attack.tMs };
}

function isStateOfSince(target: string, sinceMs: number): (event: RoundEvent) => boolean {
	return (event) => event.kind === 'process_state' && event.tMs >= sinceMs && event.target === target;
}

function firstNameOf(target: string): string {
	return target.split(',')[0]?.trim() ?? '';
}
