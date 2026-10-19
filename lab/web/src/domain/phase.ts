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

export function isAttackEvent(event: RoundEvent): boolean {
  return event.kind !== 'heal_start' && event.kind !== 'heal_complete';
}

export function isHealStart(event: RoundEvent): boolean {
  return event.kind === 'heal_start';
}

function isHealComplete(event: RoundEvent): boolean {
  return event.kind === 'heal_complete';
}
