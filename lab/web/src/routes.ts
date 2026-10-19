import type { RoundId } from './api/ids';

// The app's pages. Paths are written here and nowhere else.
export const ROUTES = {
  home: '/',
  live: '/live',
  rounds: '/rounds',
  round: '/rounds/:round',
  compare: '/compare/:left/:right',
} as const;

export function roundPath(round: RoundId): string {
  return `${ROUTES.rounds}/${encodeURIComponent(round)}`;
}

export function comparePath(left: RoundId, right: RoundId): string {
  return `/compare/${encodeURIComponent(left)}/${encodeURIComponent(right)}`;
}
