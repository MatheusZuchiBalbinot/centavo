import { useId } from 'react';

import styles from './MetricTile.module.css';

export type MetricTone = 'neutral' | 'success' | 'warning' | 'danger';

type MetricTileProps = {
  readonly label: string;
  readonly value: string;
  readonly hint: string;
  // The recent values, oldest first; the tile draws them as a sparkline.
  readonly trend: readonly number[];
  readonly tone?: MetricTone;
};

const BOX = 100;

// MetricTile is one number with its recent past under it.
export default function MetricTile({
  label,
  value,
  hint,
  trend,
  tone = 'neutral',
}: MetricTileProps): React.JSX.Element {
  return (
    <div className={styles.tile} data-tone={tone}>
      <span className={styles.label}>{label}</span>
      <span className={styles.value}>{value}</span>
      <span className={styles.hint}>{hint}</span>
      <Sparkline values={trend} />
    </div>
  );
}

function Sparkline({ values }: { values: readonly number[] }): React.JSX.Element | null {
  const gradientId = useId();

  if (values.length < 2) {
    return <div className={styles.sparkline} />;
  }

  const max = Math.max(...values, 1);
  const step = BOX / (values.length - 1);
  const points = values.map((value, index) => ({ x: index * step, y: BOX - (value / max) * (BOX - 8) - 4 }));
  const line = points
    .map((point, index) => `${index === 0 ? 'M' : 'L'}${point.x.toFixed(2)},${point.y.toFixed(2)}`)
    .join(' ');
  const last = points.at(-1);

  return (
    <div className={styles.sparkline}>
      <svg viewBox={`0 0 ${BOX} ${BOX}`} preserveAspectRatio="none" aria-hidden="true">
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" className={styles.areaTop} />
            <stop offset="100%" className={styles.areaBottom} />
          </linearGradient>
        </defs>
        <path d={`${line} L${BOX},${BOX} L0,${BOX} Z`} fill={`url(#${gradientId})`} />
        <path d={line} className={styles.line} vectorEffect="non-scaling-stroke" />
      </svg>
      {last ? <span className={styles.dot} style={{ left: `${last.x}%`, top: `${last.y}%` }} /> : null}
    </div>
  );
}
