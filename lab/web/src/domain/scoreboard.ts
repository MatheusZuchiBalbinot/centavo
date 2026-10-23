import type { RoundSummary } from '../api/rounds';
import { totalAttacks } from './attacks';
import { disabledDefenses } from './headline';

export type RoundRecord = { readonly round: RoundSummary['round']; readonly count: number };

export type Scoreboard = {
  // streak counts the newest robust rounds in a row that passed; a robust
  // round that broke ends it, and nothing else does.
  readonly streak: number;
  readonly mostAttacks: RoundRecord | null;
  readonly mostKills: RoundRecord | null;
};

// scoreboardOf reads the lab's records from rounds listed newest first.
// Only rounds with every defense on count: a control round is meant to
// break, and an inconclusive one proves nothing either way.
export function scoreboardOf(rounds: readonly RoundSummary[]): Scoreboard {
  const robust = rounds.filter(isRobust);
  const firstBroken = robust.findIndex((round) => round.verdict === 'fail');
  const sinceBreak = firstBroken === -1 ? robust : robust.slice(0, firstBroken);
  const passed = robust.filter((round) => round.verdict === 'pass');

  return {
    streak: sinceBreak.filter((round) => round.verdict === 'pass').length,
    mostAttacks: recordOf(passed, (round) => totalAttacks(round.counters)),
    mostKills: recordOf(passed, (round) => round.counters.kills),
  };
}

// isControl is a round run with some defense off, to show it is needed.
export function isControl(round: { readonly defenses: string }): boolean {
  return disabledDefenses(round.defenses).length > 0;
}

function isRobust(round: RoundSummary): boolean {
  return round.defenses !== '' && !isControl(round);
}

function recordOf(rounds: readonly RoundSummary[], countOf: (round: RoundSummary) => number): RoundRecord | null {
  let best: RoundRecord | null = null;

  for (const round of rounds) {
    const count = countOf(round);
    const isBetter = count > 0 && (best === null || count > best.count);

    if (isBetter) {
      best = { round: round.round, count };
    }
  }

  return best;
}
