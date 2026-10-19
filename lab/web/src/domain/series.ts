import type { RoundSecond } from '../api/replay';
import type { ChartPoint } from '../components/charts/AreaChart';

// The metrics the round chart can show, one at a time.
export type RoundMetric = 'operations' | 'latency' | 'trouble';

export const ROUND_METRICS: readonly RoundMetric[] = ['operations', 'latency', 'trouble'];

// The tiles show this many of the latest seconds.
const TREND_SECONDS = 60;

// seriesOf is one metric of every second: answers per second, the typical
// response time, or the operations that went wrong (refused or unanswered).
export function seriesOf(seconds: readonly RoundSecond[], metric: RoundMetric): readonly ChartPoint[] {
  return seconds.map((second) => ({ at: second.t, value: valueOf(second, metric) }));
}

// trendOf is the same metric over the last minute, for a tile's sparkline.
export function trendOf(seconds: readonly RoundSecond[], metric: RoundMetric): readonly number[] {
  return seconds.slice(-TREND_SECONDS).map((second) => valueOf(second, metric));
}

export function valueOf(second: RoundSecond, metric: RoundMetric): number {
  switch (metric) {
    case 'operations':
      return second.ok + second.fail + second.info;
    case 'latency':
      return second.p50Ms;
    case 'trouble':
      return second.fail + second.info;
  }
}
