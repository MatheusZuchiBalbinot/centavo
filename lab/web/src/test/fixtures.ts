import type { RoundId } from '../api/ids';
import type { AuditorReading, Replay, RoundEvent, RoundSecond } from '../api/replay';

// Complete fixtures: every field set, so no test leans on a missing one.

export function aSecond(t: number, ok = 20): RoundSecond {
  return { t, ok, fail: 0, info: 0, p50Ms: 8, p99Ms: 12 };
}

export function anEvent(tMs: number, kind: RoundEvent['kind'], target = 'bank-worker-1'): RoundEvent {
  return { tMs, kind, target, detail: '', duration: kind === 'freeze' ? '45s' : '', subject: '', count: 0 };
}

export function aReplay(overrides: Partial<Replay> = {}): Replay {
  return {
    round: 'r0007' as RoundId,
    scenario: 'kill-after-commit',
    seed: '0x5eed',
    seconds: [aSecond(0), aSecond(1)],
    events: [anEvent(1_500, 'kill')],
    counters: {
      kills: 1,
      failpointFires: 0,
      networkEpisodes: 0,
      databaseFaults: 0,
      freezes: 0,
      failovers: 0,
      clockSkews: 0,
      partnerLies: 0,
    },
    verdict: null,
    description: 'Mata a API logo depois do commit.',
    durationSeconds: 300,
    startedAt: new Date('2026-10-19T12:00:00Z'),
    defenses: 'robust',
    isStopped: false,
    error: '',
    checks: [],
    ...overrides,
  };
}

export function aReading(sumCents = 0, minCustomerCents: number | null = 1_000): AuditorReading {
  return { at: new Date('2026-10-19T12:00:05Z'), sumCents, minCustomerCents, isUnavailable: false };
}
