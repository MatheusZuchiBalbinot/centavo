import type { RoundId } from '../api/ids';
import type { Verdict } from '../api/rounds';

// A guess made before a surprise round: will the bank hold or break?
export type Bet = 'holds' | 'breaks';

export type BetOutcome = 'right' | 'wrong';

// The bets a person made, by round, kept in their browser.
export type BetBook = Readonly<Record<string, Bet>>;

// Only the latest rounds are kept, so the book stays small.
const MAX_BETS = 200;

export function isBet(value: unknown): value is Bet {
  return value === 'holds' || value === 'breaks';
}

export function isBetBook(value: unknown): value is BetBook {
  const isRecord = typeof value === 'object' && value !== null && !Array.isArray(value);

  return isRecord && Object.values(value).every(isBet);
}

// betOutcome settles a bet; an inconclusive round settles nothing.
export function betOutcome(bet: Bet, verdict: Verdict | null): BetOutcome | null {
  const isSettled = verdict === 'pass' || verdict === 'fail';

  if (!isSettled) {
    return null;
  }

  const isBroken = verdict === 'fail';

  return isBroken === (bet === 'breaks') ? 'right' : 'wrong';
}

// withBet adds a round's bet, dropping the oldest past the limit; betting
// again on the same round keeps the first bet.
export function withBet(book: BetBook, round: RoundId, bet: Bet): BetBook {
  if (round in book) {
    return book;
  }

  const entries = [...Object.entries(book), [round, bet] as const];

  return Object.fromEntries(entries.slice(-MAX_BETS));
}

// betScore counts the bets each way, for the rounds whose verdict is known.
export function betScore(book: BetBook, verdicts: ReadonlyMap<string, Verdict | null>): Record<BetOutcome, number> {
  const score = { right: 0, wrong: 0 };

  for (const [round, bet] of Object.entries(book)) {
    const outcome = betOutcome(bet, verdicts.get(round) ?? null);

    if (outcome !== null) {
      score[outcome] += 1;
    }
  }

  return score;
}
