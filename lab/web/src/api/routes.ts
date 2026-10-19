import type { RoundId } from './ids';

// Every path the panel calls; nothing else in the app writes an /api/ path.
export const API_ROUTES = {
  lab: '/api/lab',
  stop: '/api/lab/stop',
  attacks: '/api/attacks',
  rounds: '/api/rounds',
} as const;

export function roundEventsPath(round: RoundId): string {
  return `${API_ROUTES.rounds}/${encodeURIComponent(round)}/events`;
}

export function replayPath(round: RoundId): string {
  return `/api/replay/${encodeURIComponent(round)}`;
}

export function reportPath(round: RoundId): string {
  return `/rounds/${encodeURIComponent(round)}/report.md`;
}
