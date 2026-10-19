import type { RoundEvent } from '../api/replay';
import { messages } from '../i18n/pt-BR';
import { describeEvent } from './feed';
import { isAttackEvent, isDefenseEvent } from './phase';
import type { Phase } from './phase';

// How long the narrator keeps telling an attack, and a defense, after it.
const ATTACK_WINDOW_MS = 15_000;
const DEFENSE_WINDOW_MS = 20_000;

export type Narration = {
  readonly now: string;
  // The bank's side: a defense that just acted, or what should come of the
  // last attack while the bank has not shown it yet.
  readonly defense: { readonly text: string; readonly isWaiting: boolean } | null;
};

// narrate tells the arena's two sentences at a moment of the round.
export function narrate(events: readonly RoundEvent[], nowMs: number, phase: Phase): Narration {
  const seen = events.filter((event) => event.tMs <= nowMs);
  const attack = latest(seen, isAttackEvent, nowMs, ATTACK_WINDOW_MS);
  const defense = latest(seen, isDefenseEvent, nowMs, DEFENSE_WINDOW_MS);
  const now = attack === null ? messages.arena.phaseNow[phase] : describeEvent(attack).what;

  if (defense !== null && (attack === null || defense.tMs >= attack.tMs)) {
    return { now, defense: { text: describeEvent(defense).what, isWaiting: false } };
  }

  if (attack !== null) {
    const { expected } = describeEvent(attack);

    return { now, defense: expected === '' ? null : { text: expected, isWaiting: true } };
  }

  return { now, defense: null };
}

function latest(
  events: readonly RoundEvent[],
  isKind: (event: RoundEvent) => boolean,
  nowMs: number,
  windowMs: number,
): RoundEvent | null {
  const found = events.findLast(isKind);

  return found !== undefined && nowMs - found.tMs <= windowMs ? found : null;
}
