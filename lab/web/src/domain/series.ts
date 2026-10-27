import type { RoundSecond } from '../api/replay';
import type { ChartPoint } from '../components/charts/AreaChart';

export type RoundMetric = 'operations' | 'latency' | 'trouble';

export const ROUND_METRICS: readonly RoundMetric[] = ['operations', 'latency', 'trouble'];

const TREND_SECONDS = 60;

export function seriesOf(seconds: readonly RoundSecond[], metric: RoundMetric): readonly ChartPoint[] {
	return seconds.map((second) => ({ at: second.t, value: valueOf(second, metric) }));
}

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
