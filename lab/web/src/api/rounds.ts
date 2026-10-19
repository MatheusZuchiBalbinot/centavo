import { getJSON } from './client';
import type { ApiResult } from './client';
import { isNumber, isObject, isString } from './guards';
import { isRoundId } from './ids';
import type { RoundId } from './ids';
import { API_ROUTES } from './routes';

export type Verdict = 'pass' | 'fail' | 'inconclusive';

export type ChaosCounters = {
  readonly kills: number;
  readonly failpointFires: number;
  readonly networkEpisodes: number;
  readonly databaseFaults: number;
  readonly freezes: number;
  readonly failovers: number;
  readonly clockSkews: number;
  readonly partnerLies: number;
};

export type RoundSummary = {
  readonly round: RoundId;
  readonly scenario: string;
  readonly verdict: Verdict | null;
  readonly seconds: number;
  readonly operations: number;
  readonly counters: ChaosCounters;
};

export type RoundsPage = { readonly rounds: readonly RoundSummary[]; readonly nextCursor: RoundId | null };

export type RoundsQuery = { readonly limit: number; readonly cursor?: RoundId | null };

export function fetchRounds(query: RoundsQuery, signal?: AbortSignal): Promise<ApiResult<RoundsPage>> {
  const params = new URLSearchParams({ limit: String(query.limit) });

  if (query.cursor) {
    params.set('cursor', query.cursor);
  }

  return getJSON(`${API_ROUTES.rounds}?${params.toString()}`, parseRoundsPage, signal);
}

export function parseRoundsPage(body: unknown): RoundsPage | null {
  if (!isObject(body) || !Array.isArray(body.rounds)) {
    return null;
  }

  const rounds = body.rounds.map(parseRoundSummary);
  const nextCursor = isRoundId(body.next_cursor) ? body.next_cursor : null;

  if (!rounds.every(isPresent)) {
    return null;
  }

  return { rounds, nextCursor };
}

export function parseVerdict(value: unknown): Verdict | null {
  const isVerdict = value === 'pass' || value === 'fail' || value === 'inconclusive';

  return isVerdict ? value : null;
}

function parseRoundSummary(value: unknown): RoundSummary | null {
  if (!isObject(value)) {
    return null;
  }

  const { round, scenario, seconds, operations } = value;
  const counters = parseCounters(value.counters);

  const isComplete = isRoundId(round) && isString(scenario) && isNumber(seconds) && isNumber(operations);

  if (!isComplete || counters === null) {
    return null;
  }

  return { round, scenario, verdict: parseVerdict(value.verdict), seconds, operations, counters };
}

export function parseCounters(value: unknown): ChaosCounters | null {
  if (!isObject(value)) {
    return null;
  }

  const {
    kills,
    failpoint_fires: failpointFires,
    network_episodes: networkEpisodes,
    database_faults: databaseFaults,
    freezes,
    failovers,
    clock_skews: clockSkews,
    partner_lies: partnerLies,
  } = value;

  const isComplete =
    isNumber(kills) &&
    isNumber(failpointFires) &&
    isNumber(networkEpisodes) &&
    isNumber(databaseFaults) &&
    isNumber(freezes) &&
    isNumber(failovers) &&
    isNumber(clockSkews) &&
    isNumber(partnerLies);

  if (!isComplete) {
    return null;
  }

  return { kills, failpointFires, networkEpisodes, databaseFaults, freezes, failovers, clockSkews, partnerLies };
}

function isPresent<T>(value: T | null): value is T {
  return value !== null;
}
