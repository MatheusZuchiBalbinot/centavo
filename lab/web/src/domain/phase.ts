import type { Replay, RoundEvent } from '../api/replay';

export type Phase = 'attack' | 'heal' | 'check' | 'verdict';

export const PHASES: readonly Phase[] = ['attack', 'heal', 'check', 'verdict'];

// phaseOf reads where the round is from the cure events the history records:
// before the cure starts it is under attack, between its start and its end
// it heals, after it the checker runs, and the end of the stream closes it.
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

// The events that are not an attack: the cure, the bank defending itself,
// and the processes' state as it changes.
const NOT_ATTACKS: ReadonlySet<RoundEvent['kind']> = new Set([
  'heal_start',
  'heal_complete',
  'defense',
  'process_state',
]);

export function isAttackEvent(event: RoundEvent): boolean {
  return !NOT_ATTACKS.has(event.kind);
}

export function isDefenseEvent(event: RoundEvent): boolean {
  return event.kind === 'defense';
}

export function isStateEvent(event: RoundEvent): boolean {
  return event.kind === 'process_state';
}

export function isHealStart(event: RoundEvent): boolean {
  return event.kind === 'heal_start';
}

function isHealComplete(event: RoundEvent): boolean {
  return event.kind === 'heal_complete';
}
