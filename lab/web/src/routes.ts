import type { RoundId } from './api/ids';

// The app's pages. Paths are written here and nowhere else.
export const ROUTES = {
  home: '/',
  live: '/live',
  round: '/rounds/:round',
} as const;

export function roundPath(round: RoundId): string {
  return `/rounds/${encodeURIComponent(round)}`;
}
