import { getJSON } from './client';
import type { ApiResult } from './client';
import { isBoolean, isNumber, isObject, isOptional, isString } from './guards';
import { isRoundId } from './ids';
import type { RoundId } from './ids';
import { parseCounters, parseVerdict } from './rounds';
import type { ChaosCounters, Verdict } from './rounds';
import { replayPath } from './routes';

// One second of what the clients saw.
export type RoundSecond = {
  readonly t: number;
  readonly ok: number;
  readonly fail: number;
  readonly info: number;
  readonly p50Ms: number;
  readonly p99Ms: number;
};

// What the nemesis did, as the history names it.
type EventKind =
  | 'kill'
  | 'freeze'
  | 'failpoint_fired'
  | 'network'
  | 'database'
  | 'failover'
  | 'skew_clock'
  | 'arm_partner_modes'
  | 'heal_start'
  | 'heal_complete';

export type RoundEvent = {
  readonly tMs: number;
  readonly kind: EventKind | 'other';
  readonly target: string;
  readonly detail: string;
  readonly duration: string;
};

type GuaranteeStatus = 'ok' | 'failed' | 'not_checked';

export type GuaranteeCheck = { readonly id: string; readonly status: GuaranteeStatus; readonly violations: number };

export type Replay = {
  readonly round: RoundId;
  readonly scenario: string;
  readonly seed: string;
  readonly seconds: readonly RoundSecond[];
  readonly events: readonly RoundEvent[];
  readonly counters: ChaosCounters;
  readonly verdict: Verdict | null;
  readonly description: string;
  readonly durationSeconds: number;
  readonly startedAt: Date | null;
  readonly defenses: string;
  readonly isStopped: boolean;
  readonly error: string;
  readonly checks: readonly GuaranteeCheck[];
};

export type AuditorReading = {
  readonly at: Date;
  readonly sumCents: number;
  readonly minCustomerCents: number | null;
  readonly isUnavailable: boolean;
};

const EVENT_KINDS: ReadonlySet<string> = new Set<EventKind>([
  'kill',
  'freeze',
  'failpoint_fired',
  'network',
  'database',
  'failover',
  'skew_clock',
  'arm_partner_modes',
  'heal_start',
  'heal_complete',
]);

export function fetchReplay(round: RoundId, signal?: AbortSignal): Promise<ApiResult<Replay>> {
  return getJSON(replayPath(round), parseReplay, signal);
}

export function parseReplay(body: unknown): Replay | null {
  if (!isObject(body)) {
    return null;
  }

  const { round, scenario, seconds, events } = body;
  const counters = parseCounters(body.counters);
  const parsedSeconds = parseSeconds(seconds);
  const parsedEvents = Array.isArray(events) ? events.map(parseEvent) : null;
  // A round still running has no checks yet.
  const checks = parseChecks(Array.isArray(body.checks) ? body.checks : []);

  const isComplete =
    isRoundId(round) &&
    isString(scenario) &&
    counters !== null &&
    parsedSeconds !== null &&
    parsedEvents !== null &&
    parsedEvents.every(isPresent) &&
    checks !== null;

  if (!isComplete) {
    return null;
  }

  return {
    round,
    scenario,
    seed: stringOr(body.seed),
    seconds: parsedSeconds,
    events: parsedEvents,
    counters,
    verdict: parseVerdict(body.verdict),
    description: stringOr(body.description),
    durationSeconds: isNumber(body.duration_seconds) ? body.duration_seconds : 0,
    startedAt: parseDate(body.started_at),
    defenses: stringOr(body.defenses),
    isStopped: body.stopped === true,
    error: stringOr(body.error),
    checks,
  };
}

export function parseSeconds(value: unknown): readonly RoundSecond[] | null {
  if (!Array.isArray(value)) {
    return null;
  }

  const seconds = value.map(parseSecond);

  return seconds.every(isPresent) ? seconds : null;
}

export function parseEvent(value: unknown): RoundEvent | null {
  if (!isObject(value) || !isNumber(value.t_ms) || !isString(value.kind)) {
    return null;
  }

  const kind = isEventKind(value.kind) ? value.kind : 'other';

  return {
    tMs: value.t_ms,
    kind,
    target: stringOr(value.target),
    detail: stringOr(value.detail),
    duration: stringOr(value.duration),
  };
}

export function parseAuditorReading(value: unknown): AuditorReading | null {
  if (!isObject(value)) {
    return null;
  }

  const at = parseDate(value.at);
  const { sum_cents: sumCents, min_customer_balance_cents: minCustomer, unavailable } = value;

  const isComplete =
    at !== null && isNumber(sumCents) && isBoolean(unavailable) && isOptional(minCustomer, isNumberOrNull);

  if (!isComplete) {
    return null;
  }

  return { at, sumCents, minCustomerCents: minCustomer ?? null, isUnavailable: unavailable };
}

function parseSecond(value: unknown): RoundSecond | null {
  if (!isObject(value)) {
    return null;
  }

  const { t, ok, fail, info, p50_ms: p50Ms, p99_ms: p99Ms } = value;

  const isComplete =
    isNumber(t) && isNumber(ok) && isNumber(fail) && isNumber(info) && isNumber(p50Ms) && isNumber(p99Ms);

  if (!isComplete) {
    return null;
  }

  return { t, ok, fail, info, p50Ms, p99Ms };
}

function parseChecks(value: readonly unknown[]): readonly GuaranteeCheck[] | null {
  const checks = value.map(parseCheck);

  return checks.every(isPresent) ? checks : null;
}

function parseCheck(value: unknown): GuaranteeCheck | null {
  if (!isObject(value)) {
    return null;
  }

  const { id, status, violations } = value;

  const isComplete = isString(id) && isGuaranteeStatus(status) && isNumber(violations);

  if (!isComplete) {
    return null;
  }

  return { id, status, violations };
}

function isGuaranteeStatus(value: unknown): value is GuaranteeStatus {
  return value === 'ok' || value === 'failed' || value === 'not_checked';
}

function isEventKind(value: string): value is EventKind {
  return EVENT_KINDS.has(value);
}

function isNumberOrNull(value: unknown): value is number | null {
  return value === null || isNumber(value);
}

function parseDate(value: unknown): Date | null {
  if (!isString(value)) {
    return null;
  }

  const date = new Date(value);

  return Number.isNaN(date.getTime()) ? null : date;
}

function stringOr(value: unknown): string {
  return isString(value) ? value : '';
}

function isPresent<T>(value: T | null): value is T {
  return value !== null;
}
