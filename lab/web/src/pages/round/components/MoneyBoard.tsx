import type { AuditorReading, Replay } from '../../../api/replay';
import { formatCents } from '../../../domain/money';
import { isAttackEvent } from '../../../domain/phase';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

type MoneyBoardProps = {
  readonly replay: Replay;
  readonly reading: AuditorReading | null;
  readonly isFinished: boolean;
};

// MoneyBoard is the round's scoreboard; the money is the number that matters.
export default function MoneyBoard({ replay, reading, isFinished }: MoneyBoardProps): React.JSX.Element {
  const totals = replay.seconds.reduce(addSecond, { ok: 0, fail: 0, info: 0 });
  const operations = totals.ok + totals.fail + totals.info;
  const rate = replay.seconds.length === 0 ? 0 : Math.round(operations / replay.seconds.length);
  const attacks = replay.events.filter(isAttackEvent).length;
  const money = moneyOf(replay, reading, isFinished);

  return (
    <div className={styles.boardContainer}>
      <section className={styles.board} aria-label={messages.round.board}>
        <div className={styles.boardMoney} data-state={money.state}>
          <p className={styles.boardLabel}>{messages.round.moneyLabel}</p>
          <p className={styles.boardValue}>{money.text}</p>
          <p className={styles.boardHint}>{isFinished ? messages.round.moneyHintDone : messages.round.moneyHintLive}</p>
        </div>
        <Figure
          label={messages.round.operations}
          value={operations}
          hint={format(messages.round.operationsHint, { rate })}
        />
        <Figure label={messages.round.attacks} value={attacks} hint={messages.round.attacksHint} />
        <Figure label={messages.round.unknown} value={totals.info} hint={messages.round.unknownHint} tone="refused" />
      </section>
    </div>
  );
}

type FigureProps = { readonly label: string; readonly value: number; readonly hint: string; readonly tone?: 'refused' };

function Figure({ label, value, hint, tone }: FigureProps): React.JSX.Element {
  return (
    <div className={styles.boardFigure} data-tone={tone}>
      <p className={styles.boardLabel}>{label}</p>
      <p className={styles.boardValue}>{value.toLocaleString('pt-BR')}</p>
      <p className={styles.boardHint}>{hint}</p>
    </div>
  );
}

type Money = { readonly text: string; readonly state: 'ok' | 'broken' | 'unknown' };

// moneyOf trusts the checker once it has spoken, and the auditor before.
function moneyOf(replay: Replay, reading: AuditorReading | null, isFinished: boolean): Money {
  const conservation = replay.checks.find((check) => check.id === 'I1');

  if (isFinished && conservation) {
    return conservation.status === 'failed'
      ? { text: messages.round.moneyBrokenValue, state: 'broken' }
      : { text: formatCents(0), state: 'ok' };
  }

  const isUnread = reading === null || reading.isUnavailable;

  if (isUnread) {
    return { text: '—', state: 'unknown' };
  }

  return { text: formatCents(reading.sumCents), state: reading.sumCents === 0 ? 'ok' : 'broken' };
}

function addSecond(
  totals: { ok: number; fail: number; info: number },
  second: Replay['seconds'][number],
): { ok: number; fail: number; info: number } {
  return { ok: totals.ok + second.ok, fail: totals.fail + second.fail, info: totals.info + second.info };
}
