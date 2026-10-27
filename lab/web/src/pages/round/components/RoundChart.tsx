import { useState } from 'react';

import type { Replay } from '../../../api/replay';
import AreaChart from '../../../components/charts/AreaChart';
import { scaleMaxOf } from '../../../components/charts/scale';
import { Card } from '../../../components/ui/Card';
import { LiveIndicator } from '../../../components/ui/LiveIndicator';
import { formatClock } from '../../../domain/duration';
import { describeEvent } from '../../../domain/feed';
import type { AttackRange } from '../../../domain/highlight';
import { isAttackEvent, isHealStart } from '../../../domain/phase';
import { ROUND_METRICS, seriesOf, type RoundMetric } from '../../../domain/series';
import { attackSecondsOf, spanOf } from '../../../domain/timeline';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

const MS_PER_SECOND = 1_000;

type RoundChartProps = {
	readonly replay: Replay;
	readonly isLive: boolean;
	readonly nowSeconds: number | null;
	readonly ageSeconds: number | null;
	readonly titleId?: string;
	readonly highlight: AttackRange | null;
	readonly onHighlight: (range: AttackRange | null) => void;
};

// RoundChart is the round second by second, one metric at a time, with each
// attack marked where it landed and the cure shaded.
export default function RoundChart({
	replay,
	isLive,
	nowSeconds,
	ageSeconds,
	titleId = 'chart-title',
	highlight,
	onHighlight,
}: RoundChartProps): React.JSX.Element {
	const [metric, setMetric] = useState<RoundMetric>('operations');
	const lastEventSeconds = (replay.events.at(-1)?.tMs ?? 0) / MS_PER_SECOND;
	const span = spanOf({
		durationSeconds: attackSecondsOf(replay),
		seconds: replay.seconds,
		lastEventSeconds,
		nowSeconds,
	});
	const healStart = replay.events.find(isHealStart);
	const band = healStart ? { from: healStart.tMs / MS_PER_SECOND, to: span, label: messages.round.chartHeal } : null;
	const copy = messages.round.metrics[metric];
	const attacks = replay.events.filter(isAttackEvent);
	const markers = attacks.map((event) => ({ at: event.tMs / MS_PER_SECOND }));

	// describeMarker tells the attacks a marker stands for, in the words of the feed.
	function describeMarker(range: AttackRange): string {
		const first = attacks[range.from - 1];
		const last = attacks[range.to - 1] ?? first;

		if (first === undefined || last === undefined) {
			return '';
		}

		const count = range.to - range.from + 1;
		const label = count > 1 ? `${range.from}–${range.to}` : String(range.from);
		const from = formatClock(first.tMs / MS_PER_SECOND);
		const time = count > 1 ? `${from}–${formatClock(last.tMs / MS_PER_SECOND)}` : from;
		const what = describeEvent(first).what + (count > 1 ? ` ${format(messages.feed.times, { count })}` : '');

		return format(messages.tooltip.attackMarker, { label, time, what });
	}
	const points = seriesOf(replay.seconds, metric);
	// Scaled to the attack only: the cure's bulk re-reads would flatten it.
	const healFrom = band?.from ?? Number.POSITIVE_INFINITY;
	const attackValues = points.filter((point) => point.at < healFrom).map((point) => point.value);
	const scaleValues = attackValues.length > 0 ? attackValues : undefined;
	const isCut = scaleValues !== undefined && points.some((point) => point.value > scaleMaxOf(scaleValues));

	return (
		<Card
			title={messages.round.chartTitle}
			titleId={titleId}
			aside={
				<>
					<div className={styles.pills} role="group" aria-label={messages.round.metricLabel}>
						{ROUND_METRICS.map((option) => (
							<button key={option} type="button" className="btn btn-pill" aria-pressed={option === metric} onClick={() => setMetric(option)}>
								{messages.round.metrics[option].name}
							</button>
						))}
					</div>
					{isLive ? (
						<LiveIndicator
							state="live"
							label={messages.round.live}
							age={ageSeconds === null ? undefined : format(messages.round.age, { seconds: ageSeconds })}
						/>
					) : null}
				</>
			}
		>
			<p className={styles.chartCaption}>
				<span>
					{copy.caption}
					{isCut ? <span className={styles.chartCut}> {messages.round.chartCut}</span> : null}
				</span>
				<span className={styles.chartLegend}>
					<span className={styles.legendAttack}>{messages.round.legendAttack}</span>
					<span className={styles.legendHeal}>{messages.round.chartHeal}</span>
				</span>
			</p>
			<AreaChart
				points={points}
				scaleValues={scaleValues}
				describeMarker={describeMarker}
				highlight={highlight}
				onHighlight={onHighlight}
				domain={{ from: 0, to: span }}
				formatValue={(value) => `${Math.round(value).toLocaleString('pt-BR')}${copy.unit}`}
				formatTime={formatClock}
				markers={markers}
				band={band}
				isLive={isLive}
				label={format(messages.round.chartSummary, { metric: copy.name, duration: formatClock(replay.seconds.length) })}
			/>
		</Card>
	);
}
