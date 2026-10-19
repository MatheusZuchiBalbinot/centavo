import type { RoundSecond } from '../api/replay';

// Tick spacing of the time axis, by how long the round lasts.
const TICKS = [
  { upTo: 120, every: 15 },
  { upTo: 420, every: 30 },
  { upTo: 1_200, every: 120 },
  { upTo: Number.POSITIVE_INFINITY, every: 300 },
] as const;

const DEFAULT_TICK_SECONDS = 60;

type TimelineSpanInput = {
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

// A marker on the timeline: one attack, or several that landed too close to
// tell apart, labeled with the range of their numbers.
type Marker = { readonly x: number; readonly label: string; readonly first: number; readonly last: number };

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

  return { x: positions[first] ?? 0, label, first: first + 1, last: last + 1 };
}
