import type { RoundSecond } from '../api/replay';

// The geometry of the timeline, in viewBox units.
export const TIMELINE = {
  width: 900,
  left: 92,
  right: 12,
  attackTop: 6,
  attackHeight: 34,
  barsTop: 52,
  barsHeight: 140,
  latencyTop: 208,
  latencyHeight: 44,
  axisTop: 270,
  height: 286,
  markerRadius: 10,
  markerStagger: 7,
} as const;

// A long round would draw more bars than the chart has room for; past this
// many, seconds are added up into buckets.
const MAX_BARS = 450;

// Tick spacing of the time axis, by how long the round lasts.
const TICKS = [
  { upTo: 120, every: 15 },
  { upTo: 420, every: 30 },
  { upTo: 1_200, every: 120 },
  { upTo: Number.POSITIVE_INFINITY, every: 300 },
] as const;

const DEFAULT_TICK_SECONDS = 60;

export type TimelineSpanInput = {
  readonly durationSeconds: number;
  readonly seconds: readonly RoundSecond[];
  readonly lastEventSeconds: number;
  readonly nowSeconds: number | null;
};

// spanOf is how many seconds the axis covers: the planned attack, or more
// when the cure runs past it.
export function spanOf(input: TimelineSpanInput): number {
  const lastSecond = input.seconds.at(-1)?.t ?? 0;

  return Math.max(1, input.durationSeconds, lastSecond + 1, input.lastEventSeconds, input.nowSeconds ?? 0);
}

export function ticksOf(span: number): readonly number[] {
  const every = TICKS.find((tick) => span <= tick.upTo)?.every ?? DEFAULT_TICK_SECONDS;
  const ticks: number[] = [];

  for (let seconds = 0; seconds <= span; seconds += every) {
    ticks.push(seconds);
  }

  return ticks;
}

// A bucket is one bar: one second, or several added up.
export type Bucket = {
  readonly t: number;
  readonly width: number;
  readonly ok: number;
  readonly fail: number;
  readonly info: number;
  readonly p50Ms: number;
};

// bucketsOf groups the seconds so the chart never draws more than MAX_BARS
// bars; each bucket keeps the totals and the typical latency of its seconds.
export function bucketsOf(seconds: readonly RoundSecond[], span: number): readonly Bucket[] {
  const size = Math.max(1, Math.ceil(span / MAX_BARS));
  const buckets = new Map<number, RoundSecond[]>();

  for (const second of seconds) {
    const start = Math.floor(second.t / size) * size;
    const bucket = buckets.get(start) ?? [];

    bucket.push(second);
    buckets.set(start, bucket);
  }

  return [...buckets.entries()].map(([start, members]) => bucketOf(start, size, members));
}

function bucketOf(start: number, size: number, members: readonly RoundSecond[]): Bucket {
  const totals = members.reduce(addSecond, { ok: 0, fail: 0, info: 0, p50Ms: 0 });

  // Bars show answers per second, so a bucket of several seconds is averaged.
  return {
    t: start,
    width: size,
    ok: totals.ok / members.length,
    fail: totals.fail / members.length,
    info: totals.info / members.length,
    p50Ms: totals.p50Ms / members.length,
  };
}

function addSecond(
  totals: { ok: number; fail: number; info: number; p50Ms: number },
  second: RoundSecond,
): { ok: number; fail: number; info: number; p50Ms: number } {
  return {
    ok: totals.ok + second.ok,
    fail: totals.fail + second.fail,
    info: totals.info + second.info,
    p50Ms: totals.p50Ms + second.p50Ms,
  };
}

// A marker on the timeline: one attack, or several that landed too close to
// tell apart, labeled with the range of their numbers.
export type Marker = { readonly x: number; readonly label: string; readonly first: number };

// clusterMarkers merges attacks closer than minGap in viewBox units, so
// their numbers stay readable; positions are in the order the attacks
// happened, which is also left to right.
export function clusterMarkers(positions: readonly number[], minGap: number): readonly Marker[] {
  const markers: Marker[] = [];
  let start = 0;

  for (let index = 1; index <= positions.length; index += 1) {
    const isClusterEnd = index === positions.length || (positions[index] ?? 0) - (positions[start] ?? 0) >= minGap;

    if (isClusterEnd) {
      markers.push(markerOf(positions, start, index - 1));
      start = index;
    }
  }

  return positions.length === 0 ? [] : markers;
}

function markerOf(positions: readonly number[], first: number, last: number): Marker {
  const label = first === last ? String(first + 1) : `${first + 1}–${last + 1}`;

  return { x: positions[first] ?? 0, label, first: first + 1 };
}
