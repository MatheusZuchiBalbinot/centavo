import { useState } from 'react';

import type { Replay } from '../../../api/replay';
import AreaChart from '../../../components/charts/AreaChart';
import { Card } from '../../../components/ui/Card';
import { LiveIndicator } from '../../../components/ui/LiveIndicator';
import { formatClock } from '../../../domain/duration';
import { isAttackEvent, isHealStart } from '../../../domain/phase';
import { ROUND_METRICS, seriesOf } from '../../../domain/series';
import type { RoundMetric } from '../../../domain/series';
import { spanOf } from '../../../domain/timeline';
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
};

// RoundChart is the round second by second, one metric at a time, with each
// attack marked where it landed and the cure shaded.
export default function RoundChart({
  replay,
  isLive,
  nowSeconds,
  ageSeconds,
  titleId = 'chart-title',
}: RoundChartProps): React.JSX.Element {
  const [metric, setMetric] = useState<RoundMetric>('operations');
  const lastEventSeconds = (replay.events.at(-1)?.tMs ?? 0) / MS_PER_SECOND;
  const span = spanOf({
    durationSeconds: replay.durationSeconds,
    seconds: replay.seconds,
    lastEventSeconds,
    nowSeconds,
  });
  const healStart = replay.events.find(isHealStart);
  const markers = replay.events.filter(isAttackEvent).map((event) => ({ at: event.tMs / MS_PER_SECOND }));
  const band = healStart ? { from: healStart.tMs / MS_PER_SECOND, to: span, label: messages.round.chartHeal } : null;
  const copy = messages.round.metrics[metric];

  return (
    <Card
      title={messages.round.chartTitle}
      titleId={titleId}
      aside={
        <>
          <div className={styles.pills} role="group" aria-label={messages.round.metricLabel}>
            {ROUND_METRICS.map((option) => (
              <button
                key={option}
                type="button"
                className="btn btn-pill"
                aria-pressed={option === metric}
                onClick={() => setMetric(option)}
              >
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
        {copy.caption}
        <span className={styles.chartLegend}>
          <span className={styles.legendAttack}>{messages.round.legendAttack}</span>
          <span className={styles.legendHeal}>{messages.round.chartHeal}</span>
        </span>
      </p>
      <AreaChart
        points={seriesOf(replay.seconds, metric)}
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
