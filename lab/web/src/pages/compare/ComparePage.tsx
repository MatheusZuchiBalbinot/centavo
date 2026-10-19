import { Link, useParams } from 'react-router';

import { isRoundId } from '../../api/ids';
import type { RoundId } from '../../api/ids';
import type { Replay } from '../../api/replay';
import { Tag } from '../../components/ui/Tag';
import { expectationOf, headlineOf } from '../../domain/headline';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { roundPath } from '../../routes';
import NotFoundPage from '../not-found/NotFoundPage';
import Guarantees from '../round/components/Guarantees';
import MoneyBoard from '../round/components/MoneyBoard';
import Timeline from '../round/components/Timeline';
import styles from './ComparePage.module.css';
import { useTwoReplays } from './useTwoReplays';

// Half a page wide, the chart draws on a narrower canvas so its text stays
// the same size.
const HALF_TIMELINE_WIDTH = 560;

export default function ComparePage(): React.JSX.Element {
  const { left, right } = useParams();

  const areRounds = isRoundId(left) && isRoundId(right);

  if (!areRounds) {
    return <NotFoundPage />;
  }

  return <Comparison left={left} right={right} />;
}

function Comparison({ left, right }: { left: RoundId; right: RoundId }): React.JSX.Element {
  const replays = useTwoReplays(left, right);

  useDocumentTitle(format(messages.compare.title, { left, right }));

  return (
    <div className={styles.page}>
      <h1 id={PAGE_HEADING_ID} className={styles.headline} tabIndex={-1}>
        {messages.compare.headline}
      </h1>
      <ComparisonBody replays={replays} left={left} right={right} />
    </div>
  );
}

type ComparisonBodyProps = {
  readonly replays: ReturnType<typeof useTwoReplays>;
  readonly left: RoundId;
  readonly right: RoundId;
};

function ComparisonBody({ replays, left, right }: ComparisonBodyProps): React.JSX.Element {
  switch (replays.kind) {
    case 'loading':
      return <p className={styles.note}>{messages.compare.loading}</p>;
    case 'failed':
      return <p role="alert">{format(messages.compare.failed, { left, right })}</p>;
    case 'ready':
      return (
        <div className={styles.columns}>
          <RoundColumn replay={replays.left} />
          <RoundColumn replay={replays.right} />
        </div>
      );
  }
}

function RoundColumn({ replay }: { replay: Replay }): React.JSX.Element {
  const headline = headlineOf({ replay, phase: 'verdict', elapsedSeconds: 0, reading: null });

  return (
    <article
      className={styles.column}
      data-verdict={replay.verdict ?? 'none'}
      aria-labelledby={`${replay.round}-title`}
    >
      <header className={styles.columnHead}>
        <p className={styles.columnName}>
          <Link to={roundPath(replay.round)}>{format(messages.compare.open, { round: replay.round })}</Link> ·{' '}
          {replay.scenario}
        </p>
        <h2 id={`${replay.round}-title`} className={styles.columnHeadline}>
          {headline}
        </h2>
        <p className={styles.note}>{expectationOf(replay.defenses)}</p>
        <Tag tone={replay.verdict === 'pass' ? 'ok' : 'broken'}>
          {replay.verdict === null ? messages.verdict.none : messages.verdict[replay.verdict]}
        </Tag>
      </header>
      <MoneyBoard replay={replay} reading={null} isFinished />
      <section className={styles.chartSheet} aria-label={messages.round.chartTitle}>
        <Timeline replay={replay} nowSeconds={null} width={HALF_TIMELINE_WIDTH} />
      </section>
      <Guarantees checks={replay.checks} reading={null} isFinished={false} />
    </article>
  );
}
