import type { RoundId } from './api/ids';
import type { Bet } from './domain/bets';

// The app's pages. Paths are written here and nowhere else.
export const ROUTES = {
  home: '/',
  live: '/live',
  rounds: '/rounds',
  round: '/rounds/:round',
  compare: '/compare/:left/:right',
} as const;

// Query parameters the pages read; written here and nowhere else.
export const QUERY = { arena: 'arena', surprise: 'surprise', bet: 'bet' } as const;

// surpriseLivePath follows a surprise round into the arena, with the bet
// made in the draw, if any.
export function surpriseLivePath(bet: Bet | null): string {
  const query = new URLSearchParams({ [QUERY.arena]: '1', [QUERY.surprise]: '1' });

  if (bet !== null) {
    query.set(QUERY.bet, bet);
  }

  return `${ROUTES.live}?${query.toString()}`;
}

export function roundPath(round: RoundId): string {
  return `${ROUTES.rounds}/${encodeURIComponent(round)}`;
}

export function comparePath(left: RoundId, right: RoundId): string {
  return `/compare/${encodeURIComponent(left)}/${encodeURIComponent(right)}`;
}
