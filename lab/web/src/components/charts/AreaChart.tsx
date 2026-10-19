import { useId, useRef, useState } from 'react';

import { overlaps } from '../../domain/highlight';
import type { AttackRange } from '../../domain/highlight';
import { clusterMarkers, ticksOf } from '../../domain/timeline';
import { useHoverRange } from '../../hooks/useHoverRange';
import { useElementWidth } from '../../hooks/useElementWidth';
import styles from './AreaChart.module.css';
import { niceTicks, scaleLinear, scaleMaxOf } from './scale';

export type ChartPoint = { readonly at: number; readonly value: number };

// A moment on the time axis worth pointing at: an attack, numbered as in the
// feed.
type ChartMarker = { readonly at: number };

type ChartBand = { readonly from: number; readonly to: number; readonly label: string };

type AreaChartProps = {
  readonly points: readonly ChartPoint[];
  // The time axis, in seconds of the round, whatever part of it has data.
  readonly domain: { readonly from: number; readonly to: number };
  readonly formatValue: (value: number) => string;
  readonly formatTime: (seconds: number) => string;
  readonly markers: readonly ChartMarker[];
  readonly band: ChartBand | null;
  // isLive makes the newest point breathe while data arrives.
  readonly isLive: boolean;
  readonly label: string;
  // scaleValues set the value axis when part of the series should not: the
  // cure re-reads every transfer at once, and scaled to it the attack would
  // be a flat line. Values above the axis are cut at the top.
  readonly scaleValues?: readonly number[];
  // The attack markers are interactive: each says what it marks, and
  // pointing at one lights the same attacks elsewhere on the page.
  readonly describeMarker: (range: AttackRange) => string;
  readonly highlight: AttackRange | null;
  readonly onHighlight: (range: AttackRange | null) => void;
  readonly height?: number;
};

const DEFAULT_HEIGHT = 260;
const MARGIN = { top: 34, right: 64, bottom: 26, left: 52 } as const;
const MARKER_ROW = 12;
const MARKER_HEIGHT = 18;
// Chips closer than this would touch: they merge into one, labeled "10–14".
const MARKER_GAP_PX = 44;
// Time labels keep at least this much room each.
const MIN_TICK_SPACING_PX = 56;
const MARKER_CHAR_PX = 7;
const MARKER_PADDING_PX = 12;
const END_RADIUS = 4;
const RING = 2;

// AreaChart draws one series as a line over a soft area, with the round's
// attacks as numbered markers and the cure as a shaded band, in real pixels.
export default function AreaChart({
  points,
  domain,
  formatValue,
  formatTime,
  markers,
  band,
  isLive,
  label,
  scaleValues,
  describeMarker,
  highlight,
  onHighlight,
  height = DEFAULT_HEIGHT,
}: AreaChartProps): React.JSX.Element {
  const frame = useRef<HTMLDivElement>(null);
  const width = useElementWidth(frame);
  const gradientId = useId();
  const clipId = useId();
  const [hoverAt, setHoverAt] = useState<number | null>(null);

  const ticks = niceTicks(scaleMaxOf(scaleValues ?? points.map(valueOf)));
  const maxValue = ticks.at(-1) ?? 1;
  const plotRight = Math.max(width - MARGIN.right, MARGIN.left + 1);
  const plotBottom = height - MARGIN.bottom;
  const x = scaleLinear(domain.from, domain.to, MARGIN.left, plotRight);
  // Values past the axis stay on its top edge instead of leaving the plot.
  const toY = scaleLinear(0, maxValue, plotBottom, MARGIN.top);
  const y = (value: number): number => Math.max(MARGIN.top, toY(value));
  const line = points
    .map((point, index) => `${index === 0 ? 'M' : 'L'}${x(point.at).toFixed(1)},${y(point.value).toFixed(1)}`)
    .join(' ');
  const first = points[0];
  const last = points.at(-1);
  const area = first && last ? `${line} L${x(last.at)},${plotBottom} L${x(first.at)},${plotBottom} Z` : '';
  const clusters = clusterMarkers(
    markers.map((marker) => x(marker.at)),
    MARKER_GAP_PX,
  );
  const hovered = hoverAt === null ? null : closestPoint(points, hoverAt);

  function handlePointerMove(event: React.PointerEvent<SVGSVGElement>): void {
    const { left, top } = event.currentTarget.getBoundingClientRect();

    // Over the marker row the attack's own tooltip speaks; the crosshair waits.
    if (event.clientY - top < MARGIN.top) {
      setHoverAt(null);

      return;
    }

    const at =
      domain.from + ((event.clientX - left - MARGIN.left) / (plotRight - MARGIN.left)) * (domain.to - domain.from);

    setHoverAt(at);
  }

  return (
    <div ref={frame} className={styles.frame} data-live={isLive}>
      <svg
        className={styles.svg}
        width="100%"
        height={height}
        role="img"
        aria-label={label}
        onPointerMove={handlePointerMove}
        onPointerLeave={() => setHoverAt(null)}
      >
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" className={styles.areaTop} />
            <stop offset="100%" className={styles.areaBottom} />
          </linearGradient>
          <clipPath id={clipId}>
            <rect
              x={MARGIN.left}
              y={MARGIN.top}
              width={Math.max(plotRight - MARGIN.left, 0)}
              height={plotBottom - MARGIN.top}
            />
          </clipPath>
        </defs>
        {band ? (
          <g className={styles.band}>
            <rect
              x={x(band.from)}
              y={MARGIN.top}
              width={Math.max(1, x(band.to) - x(band.from))}
              height={plotBottom - MARGIN.top}
            />
            <text x={x(band.from) + 8} y={MARGIN.top + 14}>
              {band.label}
            </text>
          </g>
        ) : null}
        {ticks.map((tick) => (
          <g key={tick}>
            <line className={styles.grid} x1={MARGIN.left} x2={plotRight} y1={y(tick)} y2={y(tick)} />
            <text className={styles.tick} x={MARGIN.left - 8} y={y(tick)} textAnchor="end" dominantBaseline="middle">
              {formatValue(tick)}
            </text>
          </g>
        ))}
        {timeTicks(domain.to - domain.from, plotRight - MARGIN.left).map((seconds) => (
          <text key={seconds} className={styles.tick} x={x(domain.from + seconds)} y={height - 6} textAnchor="middle">
            {formatTime(domain.from + seconds)}
          </text>
        ))}
        <path className={styles.area} d={area} fill={`url(#${gradientId})`} clipPath={`url(#${clipId})`} />
        <path className={styles.line} d={line} clipPath={`url(#${clipId})`} />
        {clusters.map((cluster) => (
          <MarkerChip
            key={cluster.first}
            x={cluster.x}
            label={cluster.label}
            range={{ from: cluster.first, to: cluster.last }}
            bottom={plotBottom}
            description={describeMarker({ from: cluster.first, to: cluster.last })}
            isHot={overlaps(highlight, cluster.first, cluster.last)}
            onHighlight={onHighlight}
          />
        ))}
        {last ? <EndPoint x={x(last.at)} y={y(last.value)} label={formatValue(last.value)} isLive={isLive} /> : null}
        {hovered ? <Crosshair x={x(hovered.at)} y={y(hovered.value)} top={MARGIN.top} bottom={plotBottom} /> : null}
      </svg>
      {hovered ? (
        <div className={styles.tooltip} style={{ left: `${x(hovered.at)}px` }}>
          <span className={styles.tooltipTime}>{formatTime(hovered.at)}</span>
          <strong>{formatValue(hovered.value)}</strong>
        </div>
      ) : null}
    </div>
  );
}

type MarkerChipProps = {
  readonly x: number;
  readonly label: string;
  readonly range: AttackRange;
  readonly bottom: number;
  readonly description: string;
  readonly isHot: boolean;
  readonly onHighlight: (range: AttackRange | null) => void;
};

// MarkerChip is focusable, so a keyboard reaches each attack and its
// tooltip the way a pointer does.
function MarkerChip({ x, label, range, bottom, description, isHot, onHighlight }: MarkerChipProps): React.JSX.Element {
  const width = Math.max(MARKER_HEIGHT, label.length * MARKER_CHAR_PX + MARKER_PADDING_PX);
  const ref = useRef<SVGGElement>(null);

  useHoverRange(ref, range, onHighlight);

  return (
    <g
      ref={ref}
      className={styles.marker}
      data-hot={isHot}
      data-tooltip={description}
      tabIndex={0}
      aria-label={description}
    >
      <line x1={x} x2={x} y1={MARKER_ROW + MARKER_HEIGHT} y2={bottom} />
      <rect x={x - width / 2} y={MARKER_ROW} width={width} height={MARKER_HEIGHT} rx={MARKER_HEIGHT / 2} />
      <text x={x} y={MARKER_ROW + MARKER_HEIGHT / 2} textAnchor="middle" dominantBaseline="central">
        {label}
      </text>
    </g>
  );
}

type EndPointProps = { readonly x: number; readonly y: number; readonly label: string; readonly isLive: boolean };

function EndPoint({ x, y, label, isLive }: EndPointProps): React.JSX.Element {
  return (
    <g>
      {isLive ? <circle className={styles.pulse} cx={x} cy={y} r={END_RADIUS} /> : null}
      <circle className={styles.ring} cx={x} cy={y} r={END_RADIUS + RING} />
      <circle className={styles.dot} cx={x} cy={y} r={END_RADIUS} />
      <text className={styles.endLabel} x={x + END_RADIUS + 6} y={y} dominantBaseline="middle">
        {label}
      </text>
    </g>
  );
}

type CrosshairProps = { readonly x: number; readonly y: number; readonly top: number; readonly bottom: number };

function Crosshair({ x, y, top, bottom }: CrosshairProps): React.JSX.Element {
  return (
    <g className={styles.crosshair}>
      <line x1={x} x2={x} y1={top} y2={bottom} />
      <circle cx={x} cy={y} r={END_RADIUS} />
    </g>
  );
}

// timeTicks thins the time labels until they fit the plot's width.
function timeTicks(span: number, plotWidth: number): readonly number[] {
  const ticks = ticksOf(span);
  const room = ticks.length > 1 ? plotWidth / (ticks.length - 1) : plotWidth;
  const every = Math.max(1, Math.ceil(MIN_TICK_SPACING_PX / room));

  return ticks.filter((_, index) => index % every === 0);
}

function closestPoint(points: readonly ChartPoint[], at: number): ChartPoint | null {
  let best: ChartPoint | null = null;

  for (const point of points) {
    const isCloser = best === null || Math.abs(point.at - at) < Math.abs(best.at - at);

    if (isCloser) {
      best = point;
    }
  }

  return best;
}

function valueOf(point: ChartPoint): number {
  return point.value;
}
