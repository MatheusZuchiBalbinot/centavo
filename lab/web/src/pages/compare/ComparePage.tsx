import { useState } from 'react';
import { Link, useParams } from 'react-router';

import { isRoundId } from '../../api/ids';
import type { RoundId } from '../../api/ids';
import type { Replay } from '../../api/replay';
import { LinkButton } from '../../components/ui/Button';
import VerdictBadge from '../../components/VerdictBadge';
import { expectationOf, headlineOf } from '../../domain/headline';
import type { AttackRange } from '../../domain/highlight';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { roundPath, ROUTES } from '../../routes';
import NotFoundPage from '../not-found/NotFoundPage';
import Guarantees from '../round/components/Guarantees';
import RoundChart from '../round/components/RoundChart';
import RoundTiles from '../round/components/RoundTiles';
import styles from './ComparePage.module.css';
import { useTwoReplays } from './useTwoReplays';
import type { TwoReplays } from './useTwoReplays';

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
    <>
      <header className="page-header">
        <div>
          <h1 id={PAGE_HEADING_ID} className="page-title" tabIndex={-1}>
            {messages.compare.headline}
          </h1>
          <p className="page-desc">{messages.compare.lead}</p>
        </div>
      </header>
      <ComparisonBody replays={replays} />
    </>
  );
}

function ComparisonBody({ replays }: { replays: TwoReplays }): React.JSX.Element {
  switch (replays.kind) {
    case 'loading':
      return <p className="muted">{messages.compare.loading}</p>;
    case 'missing':
      return (
        <div role="alert" className={styles.problem}>
          <p>{missingText(replays.rounds)}</p>
          <LinkButton to={ROUTES.rounds}>{messages.compare.toList}</LinkButton>
        </div>
      );
    case 'failed':
      return <p role="alert">{messages.compare.failed}</p>;
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
  const [highlight, setHighlight] = useState<AttackRange | null>(null);
  const headline = headlineOf({ replay, phase: 'verdict', elapsedSeconds: 0, reading: null });
  const titleId = `${replay.round}-title`;

  return (
    <article className={styles.column} aria-labelledby={titleId}>
      <header className={styles.columnHead} data-verdict={replay.verdict ?? 'none'}>
        <div className={styles.columnTop}>
          <Link to={roundPath(replay.round)} className="mono">
            {replay.round}
          </Link>
          <span className="faint mono">{replay.scenario}</span>
          <VerdictBadge verdict={replay.verdict} />
        </div>
        <h2 id={titleId} className={styles.columnHeadline}>
          {headline}
        </h2>
        <p className="muted">{expectationOf(replay.defenses)}</p>
      </header>
      <RoundTiles replay={replay} reading={null} isFinished />
      <RoundChart
        replay={replay}
        isLive={false}
        nowSeconds={null}
        ageSeconds={null}
        titleId={`${replay.round}-chart`}
        highlight={highlight}
        onHighlight={setHighlight}
      />
      <Guarantees checks={replay.checks} reading={null} isFinished={false} titleId={`${replay.round}-guarantees`} />
    </article>
  );
}

function missingText(rounds: readonly RoundId[]): string {
  const [first, second] = rounds;

  if (second === undefined) {
    return format(messages.compare.missingOne, { round: first ?? '' });
  }

  return format(messages.compare.missingBoth, { left: first ?? '', right: second });
}
