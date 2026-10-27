import type { Replay, RoundEvent } from '../api/replay';

export type Phase = 'attack' | 'heal' | 'check' | 'verdict';

export const PHASES: readonly Phase[] = ['attack', 'heal', 'check', 'verdict'];

export function phaseOf(replay: Replay, isFinished: boolean): Phase {
	if (isFinished) {
		return 'verdict';
	}

	const hasHealEnded = replay.events.some(isHealComplete);

	if (hasHealEnded) {
		return 'check';
	}

	const hasHealStarted = replay.events.some(isHealStart);

	if (hasHealStarted) {
		return 'heal';
	}

	return 'attack';
}

const NOT_ATTACKS: ReadonlySet<RoundEvent['kind']> = new Set(['heal_start', 'heal_complete', 'defense', 'process_state']);

export function isAttackEvent(event: RoundEvent): boolean {
	return !NOT_ATTACKS.has(event.kind) && !isNetworkEnd(event);
}

// The map heals the link; nothing else tells it.
export function isNetworkEnd(event: RoundEvent): boolean {
	return event.kind === 'network' && event.subject === 'end';
}

export function isDefenseEvent(event: RoundEvent): boolean {
	return event.kind === 'defense';
}

export function isStateEvent(event: RoundEvent): boolean {
	return event.kind === 'process_state' || isNetworkEnd(event);
}

export function isHealStart(event: RoundEvent): boolean {
	return event.kind === 'heal_start';
}

function isHealComplete(event: RoundEvent): boolean {
	return event.kind === 'heal_complete';
}
