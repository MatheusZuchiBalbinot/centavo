import type { AttackMenu } from '../api/attacks';
import type { AttackId } from '../api/ids';

// A number in [0, 1), like Math.random; tests pass their own.
export type Random = () => number;

export type SurpriseDraw = { readonly attack: AttackId; readonly durationSeconds: number };

// The roulette's cards, with the winner near the end so cards keep coming
// after it while the reel slows down.
export type Reel = { readonly cards: readonly AttackId[]; readonly winnerIndex: number };

// The quiet round attacks nothing, so it is no surprise.
const CALM = 'calm';
// A surprise is short: long enough to see attacks land, short enough to stay.
const SURPRISE_DURATIONS: ReadonlySet<number> = new Set([180, 300]);
const REEL_CARDS = 24;
const CARDS_AFTER_WINNER = 2;

// drawSurprise picks an attack and a duration from the lab's menu; null when
// the menu has nothing to draw.
export function drawSurprise(menu: AttackMenu, random: Random): SurpriseDraw | null {
  const attacks = drawableAttacks(menu);
  const shortDurations = menu.durationsSeconds.filter((seconds) => SURPRISE_DURATIONS.has(seconds));
  const durations = shortDurations.length > 0 ? shortDurations : menu.durationsSeconds;
  const attack = pick(attacks, random);
  const durationSeconds = pick(durations, random);

  if (attack === undefined || durationSeconds === undefined) {
    return null;
  }

  return { attack, durationSeconds };
}

export function drawableAttacks(menu: AttackMenu): readonly AttackId[] {
  return menu.attacks.map((attack) => attack.id).filter((id) => id !== CALM);
}

// reelOf fills the roulette with random cards and puts the winner where the
// reel stops.
export function reelOf(attacks: readonly AttackId[], winner: AttackId, random: Random): Reel {
  const winnerIndex = REEL_CARDS - CARDS_AFTER_WINNER - 1;
  const cards = Array.from({ length: REEL_CARDS }, (_, index) =>
    index === winnerIndex ? winner : (pick(attacks, random) ?? winner),
  );

  return { cards, winnerIndex };
}

function pick<T>(values: readonly T[], random: Random): T | undefined {
  return values[Math.floor(random() * values.length)];
}
