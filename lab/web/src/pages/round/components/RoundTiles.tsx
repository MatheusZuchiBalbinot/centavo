import type { AuditorReading, Replay } from '../../../api/replay';
import MetricTile from '../../../components/charts/MetricTile';
import type { MetricTone } from '../../../components/charts/MetricTile';
import { formatCents } from '../../../domain/money';
import { trendOf, valueOf } from '../../../domain/series';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

const MS = ' ms';

type RoundTilesProps = {
  readonly replay: Replay;
  readonly reading: AuditorReading | null;
  readonly isFinished: boolean;
};

// RoundTiles is the round's scoreboard: the money first, then how the
// clients are being served.
export default function RoundTiles({ replay, reading, isFinished }: RoundTilesProps): React.JSX.Element {
  const latest = replay.seconds.at(-1);
  const totals = replay.seconds.reduce(addSecond, { all: 0, trouble: 0 });
  const money = moneyOf(replay, reading, isFinished);
  const operationsTrend = trendOf(replay.seconds, 'operations');
  // The auditor reads one sum at a time; a flat line at its value says the
  // money stayed put while the clients kept going.
  const moneyTrend = operationsTrend.map(() => Math.abs(reading?.sumCents ?? 0));

  return (
    <section className={styles.tiles} aria-label={messages.round.board}>
      <MetricTile
        label={messages.round.moneyLabel}
        value={money.text}
        hint={isFinished ? messages.round.moneyHintDone : messages.round.moneyHintLive}
        trend={moneyTrend}
        tone={money.tone}
        help={messages.tooltip.money}
      />
      <MetricTile
        label={isFinished ? messages.round.rateAverageLabel : messages.round.rateLabel}
        value={rateText(replay, latest, isFinished)}
        hint={format(messages.round.rateHint, { total: totals.all.toLocaleString('pt-BR') })}
        help={messages.tooltip.rate}
        trend={operationsTrend}
      />
      <MetricTile
        label={messages.round.latencyLabel}
        value={latest ? `${Math.round(latest.p50Ms).toLocaleString('pt-BR')}${MS}` : '—'}
        hint={messages.round.latencyHint}
        help={messages.tooltip.latency}
        trend={trendOf(replay.seconds, 'latency')}
      />
      <MetricTile
        label={messages.round.troubleLabel}
        value={totals.trouble.toLocaleString('pt-BR')}
        hint={messages.round.troubleHint}
        help={messages.tooltip.trouble}
        trend={trendOf(replay.seconds, 'trouble')}
        tone={totals.trouble > 0 ? 'warning' : 'neutral'}
      />
    </section>
  );
}

type Money = { readonly text: string; readonly tone: MetricTone };

// moneyOf trusts the checker once it has spoken, and the auditor before.
function moneyOf(replay: Replay, reading: AuditorReading | null, isFinished: boolean): Money {
  const conservation = replay.checks.find((check) => check.id === 'I1');

  if (isFinished && conservation) {
    return conservation.status === 'failed'
      ? { text: messages.round.moneyBrokenValue, tone: 'danger' }
      : { text: formatCents(0), tone: 'success' };
  }

  const isUnread = reading === null || reading.isUnavailable;

  if (isUnread) {
    return { text: '—', tone: 'neutral' };
  }

  return { text: formatCents(reading.sumCents), tone: reading.sumCents === 0 ? 'success' : 'danger' };
}

// rateText is the latest second while the round runs, and the average of
// the whole round once it is over.
function rateText(replay: Replay, latest: Replay['seconds'][number] | undefined, isFinished: boolean): string {
  if (latest === undefined) {
    return '—';
  }

  const rate = isFinished
    ? replay.seconds.reduce(addSecond, { all: 0, trouble: 0 }).all / replay.seconds.length
    : valueOf(latest, 'operations');

  return `${Math.round(rate).toLocaleString('pt-BR')}/s`;
}

function addSecond(
  totals: { all: number; trouble: number },
  second: Replay['seconds'][number],
): { all: number; trouble: number } {
  return { all: totals.all + valueOf(second, 'operations'), trouble: totals.trouble + valueOf(second, 'trouble') };
}
