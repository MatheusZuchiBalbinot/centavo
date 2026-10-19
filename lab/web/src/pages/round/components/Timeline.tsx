import type { Replay } from '../../../api/replay';
import { formatClock, formatDuration } from '../../../domain/duration';
import { isAttackEvent, isHealStart } from '../../../domain/phase';
import { bucketsOf, clusterMarkers, spanOf, ticksOf, TIMELINE } from '../../../domain/timeline';
import type { Bucket, Marker } from '../../../domain/timeline';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

const MS_PER_SECOND = 1_000;
// Attacks whose markers would touch are drawn as one, labeled "10–14".
const MARKER_GAP = TIMELINE.markerRadius * 2.2;
// A label wider than one digit gets a pill instead of a circle.
const PILL_CHAR_WIDTH = 6.5;
const PILL_PADDING = 8;
const MIN_BAR_WIDTH = 1;
const BAR_GAP = 0.6;

// width is the viewBox width: a narrower one keeps the text readable when the
// chart sits in half a page.
type TimelineProps = { readonly replay: Replay; readonly nowSeconds: number | null; readonly width?: number };

// Timeline draws the round on one time axis: attacks numbered as in the feed
// on top, the answers per second in the middle, the typical response time
// below, and the cure shaded across all three.
export default function Timeline({ replay, nowSeconds, width = TIMELINE.width }: TimelineProps): React.JSX.Element {
  const attacks = replay.events.filter(isAttackEvent);
  const lastEventSeconds = (replay.events.at(-1)?.tMs ?? 0) / MS_PER_SECOND;
  const spanInput = { durationSeconds: replay.durationSeconds, seconds: replay.seconds, lastEventSeconds, nowSeconds };
  const span = spanOf(spanInput);
  const plot = width - TIMELINE.left - TIMELINE.right;
  const buckets = bucketsOf(replay.seconds, span);
  const scale = { x: (seconds: number) => TIMELINE.left + (seconds / span) * plot, secondWidth: plot / span };
  const healStart = replay.events.find(isHealStart);
  const markers = clusterMarkers(attacks.map(xOfEvent(scale.x)), MARKER_GAP);

  return (
    <figure className={styles.chart}>
      <svg viewBox={`0 0 ${width} ${TIMELINE.height}`} role="img" aria-label={summaryOf(replay)}>
        {healStart ? <HealBand startSeconds={healStart.tMs / MS_PER_SECOND} span={span} x={scale.x} /> : null}
        <Axis span={span} x={scale.x} />
        <Bars buckets={buckets} x={scale.x} secondWidth={scale.secondWidth} />
        <Latency buckets={buckets} x={scale.x} />
        {markers.map((marker, index) => (
          <AttackMarker key={marker.first} marker={marker} index={index} />
        ))}
        {nowSeconds === null ? null : <NowLine at={scale.x(nowSeconds)} />}
      </svg>
      <figcaption className={styles.legend}>
        <span data-swatch="ok">{messages.round.legendOk}</span>
        <span data-swatch="refused">{messages.round.legendRefused}</span>
        <span data-swatch="unknown">{messages.round.legendUnknown}</span>
        <span data-swatch="attack">{messages.round.legendAttack}</span>
      </figcaption>
    </figure>
  );
}

type Scale = (seconds: number) => number;

function HealBand({ startSeconds, span, x }: { startSeconds: number; span: number; x: Scale }): React.JSX.Element {
  const top = TIMELINE.attackTop;
  const height = TIMELINE.latencyTop + TIMELINE.latencyHeight - top;

  return (
    <g className={styles.heal}>
      <rect x={x(startSeconds)} y={top} width={Math.max(MIN_BAR_WIDTH, x(span) - x(startSeconds))} height={height} />
      <text x={x(startSeconds) + 6} y={TIMELINE.barsTop - 4}>
        {messages.round.chartHeal}
      </text>
    </g>
  );
}

function Axis({ span, x }: { span: number; x: Scale }): React.JSX.Element {
  const bottom = TIMELINE.latencyTop + TIMELINE.latencyHeight;
  const barsBottom = TIMELINE.barsTop + TIMELINE.barsHeight;

  return (
    <g className={styles.axis}>
      <line className={styles.baseline} x1={x(0)} x2={x(span)} y1={barsBottom} y2={barsBottom} />
      <line className={styles.baseline} x1={x(0)} x2={x(span)} y1={bottom} y2={bottom} />
      <text x={0} y={TIMELINE.barsTop + 14}>
        {messages.round.laneOperations}
      </text>
      <text x={0} y={TIMELINE.latencyTop + 14}>
        {messages.round.laneLatency}
      </text>
      {ticksOf(span).map((seconds) => (
        <g key={seconds}>
          <line x1={x(seconds)} x2={x(seconds)} y1={TIMELINE.barsTop} y2={bottom} />
          <text x={x(seconds)} y={TIMELINE.axisTop + 8} textAnchor="middle">
            {formatClock(seconds)}
          </text>
        </g>
      ))}
    </g>
  );
}

type BarsProps = { readonly buckets: readonly Bucket[]; readonly x: Scale; readonly secondWidth: number };

function Bars({ buckets, x, secondWidth }: BarsProps): React.JSX.Element {
  const peak = Math.max(1, ...buckets.map(totalOf));
  const scale = TIMELINE.barsHeight / peak;
  const bottom = TIMELINE.barsTop + TIMELINE.barsHeight;

  return (
    <g className={styles.bars}>
      {buckets.map((bucket) => (
        <BarStack key={bucket.t} bucket={bucket} x={x} secondWidth={secondWidth} scale={scale} bottom={bottom} />
      ))}
    </g>
  );
}

type BarStackProps = {
  readonly bucket: Bucket;
  readonly x: Scale;
  readonly secondWidth: number;
  readonly scale: number;
  readonly bottom: number;
};

// BarStack is one bar: the answered at the bottom, the refused and the
// unanswered stacked on top.
function BarStack({ bucket, x, secondWidth, scale, bottom }: BarStackProps): React.JSX.Element {
  const left = x(bucket.t);
  const width = Math.max(MIN_BAR_WIDTH, bucket.width * secondWidth - BAR_GAP);
  const okHeight = bucket.ok * scale;
  const failHeight = bucket.fail * scale;
  const infoHeight = bucket.info * scale;

  return (
    <g>
      <rect data-part="ok" x={left} y={bottom - okHeight} width={width} height={okHeight} />
      <rect data-part="refused" x={left} y={bottom - okHeight - failHeight} width={width} height={failHeight} />
      <rect
        data-part="unknown"
        x={left}
        y={bottom - okHeight - failHeight - infoHeight}
        width={width}
        height={infoHeight}
      />
    </g>
  );
}

function Latency({ buckets, x }: { buckets: readonly Bucket[]; x: Scale }): React.JSX.Element {
  const peak = Math.max(1, ...buckets.map(latencyOf));
  const bottom = TIMELINE.latencyTop + TIMELINE.latencyHeight;
  const points = buckets.map(latencyPoint(x, peak, bottom)).join(' ');

  return (
    <g className={styles.latency}>
      <polyline points={points} />
    </g>
  );
}

function latencyPoint(x: Scale, peak: number, bottom: number): (bucket: Bucket) => string {
  return (bucket) => {
    const left = x(bucket.t + bucket.width / 2);
    const top = bottom - (bucket.p50Ms / peak) * TIMELINE.latencyHeight;

    return `${left.toFixed(1)},${top.toFixed(1)}`;
  };
}

// Markers alternate above and below the lane's middle, since attacks often
// come in pairs a fraction of a second apart.
function AttackMarker({ marker, index }: { marker: Marker; index: number }): React.JSX.Element {
  const middle = TIMELINE.attackTop + TIMELINE.attackHeight / 2;
  const center = index % 2 === 0 ? middle - TIMELINE.markerStagger : middle + TIMELINE.markerStagger;
  const isGroup = marker.label.length > 2;
  const width = Math.max(TIMELINE.markerRadius * 2, marker.label.length * PILL_CHAR_WIDTH + PILL_PADDING);
  const radius = TIMELINE.markerRadius;

  return (
    <g className={styles.marker}>
      <line x1={marker.x} x2={marker.x} y1={center} y2={TIMELINE.latencyTop + TIMELINE.latencyHeight} />
      {isGroup ? (
        <rect x={marker.x - width / 2} y={center - radius} width={width} height={radius * 2} rx={radius} />
      ) : (
        <circle cx={marker.x} cy={center} r={radius} />
      )}
      <text x={marker.x} y={center + 4} textAnchor="middle">
        {marker.label}
      </text>
    </g>
  );
}

function xOfEvent(x: Scale): (event: Replay['events'][number]) => number {
  return (event) => x(event.tMs / MS_PER_SECOND);
}

function NowLine({ at }: { at: number }): React.JSX.Element {
  return (
    <g className={styles.now}>
      <line x1={at} x2={at} y1={TIMELINE.attackTop} y2={TIMELINE.latencyTop + TIMELINE.latencyHeight} />
      <text x={at + 5} y={TIMELINE.barsTop - 4}>
        {messages.round.chartNow}
      </text>
    </g>
  );
}

function summaryOf(replay: Replay): string {
  const totals = replay.seconds.reduce(addAnswers, { ok: 0, refused: 0, unknown: 0 });

  return format(messages.round.chartSummary, {
    ok: totals.ok.toLocaleString('pt-BR'),
    refused: totals.refused.toLocaleString('pt-BR'),
    unknown: totals.unknown.toLocaleString('pt-BR'),
    duration: formatDuration(replay.seconds.length),
  });
}

type Answers = { readonly ok: number; readonly refused: number; readonly unknown: number };

function addAnswers(sum: Answers, second: Replay['seconds'][number]): Answers {
  return { ok: sum.ok + second.ok, refused: sum.refused + second.fail, unknown: sum.unknown + second.info };
}

function totalOf(bucket: Bucket): number {
  return bucket.ok + bucket.fail + bucket.info;
}

function latencyOf(bucket: Bucket): number {
  return bucket.p50Ms;
}
