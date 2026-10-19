import { useState } from 'react';
import { Link } from 'react-router';

import type { RoundId } from '../../api/ids';
import type { RoundSummary } from '../../api/rounds';
import { Button, LinkButton } from '../../components/ui/Button';
import { Tag } from '../../components/ui/Tag';
import type { TagTone } from '../../components/ui/Tag';
import { totalAttacks } from '../../domain/attacks';
import { formatDuration } from '../../domain/duration';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { comparePath, roundPath } from '../../routes';
import styles from './RoundsPage.module.css';
import { useRoundPages } from './useRoundPages';

// Comparing takes exactly two rounds.
const ROUNDS_COMPARED = 2;

export default function RoundsPage(): React.JSX.Element {
  useDocumentTitle(messages.rounds.title);

  const pages = useRoundPages();
  const [picked, setPicked] = useState<readonly RoundId[]>([]);

  function handleToggle(round: RoundId): void {
    setPicked((current) => togglePick(current, round));
  }

  return (
    <div className={styles.page}>
      <h1 id={PAGE_HEADING_ID} className={styles.headline} tabIndex={-1}>
        {messages.rounds.headline}
      </h1>
      <p className={styles.lead}>{messages.rounds.lead}</p>
      <CompareBar picked={picked} />
      <RoundsBody pages={pages} picked={picked} onToggle={handleToggle} />
    </div>
  );
}

function CompareBar({ picked }: { picked: readonly RoundId[] }): React.JSX.Element {
  const [left, right] = picked;

  if (left === undefined || right === undefined) {
    return <p className={styles.hint}>{messages.rounds.compareHint}</p>;
  }

  return (
    <LinkButton to={comparePath(left, right)} variant="primary" className={styles.compare}>
      {messages.rounds.compare}
    </LinkButton>
  );
}

type RoundsBodyProps = {
  readonly pages: ReturnType<typeof useRoundPages>;
  readonly picked: readonly RoundId[];
  readonly onToggle: (round: RoundId) => void;
};

function RoundsBody({ pages, picked, onToggle }: RoundsBodyProps): React.JSX.Element {
  switch (pages.kind) {
    case 'loading':
      return <p className={styles.hint}>{messages.rounds.loading}</p>;
    case 'failed':
      return <p role="alert">{messages.rounds.failed}</p>;
    case 'ready':
      return <RoundsTable pages={pages} picked={picked} onToggle={onToggle} />;
  }
}

type RoundsTableProps = {
  readonly pages: Extract<ReturnType<typeof useRoundPages>, { kind: 'ready' }>;
  readonly picked: readonly RoundId[];
  readonly onToggle: (round: RoundId) => void;
};

function RoundsTable({ pages, picked, onToggle }: RoundsTableProps): React.JSX.Element {
  if (pages.rounds.length === 0) {
    return <p className={styles.hint}>{messages.rounds.empty}</p>;
  }

  return (
    <>
      <div className={styles.tableWrap}>
        <table className={styles.table}>
          <caption className="visually-hidden">{messages.rounds.caption}</caption>
          <thead>
            <tr>
              <th scope="col">{messages.rounds.pick}</th>
              <th scope="col">{messages.rounds.round}</th>
              <th scope="col">{messages.rounds.scenario}</th>
              <th scope="col">{messages.rounds.duration}</th>
              <th scope="col" className={styles.number}>
                {messages.rounds.operations}
              </th>
              <th scope="col" className={styles.number}>
                {messages.rounds.attacks}
              </th>
              <th scope="col">{messages.rounds.verdict}</th>
            </tr>
          </thead>
          <tbody>
            {pages.rounds.map((round) => (
              <RoundRow key={round.round} round={round} picked={picked} onToggle={onToggle} />
            ))}
          </tbody>
        </table>
      </div>
      {pages.hasMore ? (
        <Button onClick={pages.loadMore} disabled={pages.isLoadingMore} className={styles.more}>
          {messages.rounds.more}
        </Button>
      ) : null}
    </>
  );
}

type RoundRowProps = {
  readonly round: RoundSummary;
  readonly picked: readonly RoundId[];
  readonly onToggle: (round: RoundId) => void;
};

function RoundRow({ round, picked, onToggle }: RoundRowProps): React.JSX.Element {
  const isPicked = picked.includes(round.round);
  const isFull = picked.length >= ROUNDS_COMPARED && !isPicked;

  return (
    <tr data-picked={isPicked}>
      <td>
        <input
          type="checkbox"
          className={styles.checkbox}
          checked={isPicked}
          disabled={isFull}
          aria-label={format(messages.rounds.pickLabel, { round: round.round })}
          onChange={() => onToggle(round.round)}
        />
      </td>
      <th scope="row">
        <Link to={roundPath(round.round)}>{round.round}</Link>
      </th>
      <td>{round.scenario}</td>
      <td>{formatDuration(round.seconds)}</td>
      <td className={styles.number}>{round.operations.toLocaleString('pt-BR')}</td>
      <td className={styles.number}>{totalAttacks(round.counters).toLocaleString('pt-BR')}</td>
      <td>
        <Tag tone={toneOf(round)}>
          {round.verdict === null ? messages.verdict.none : messages.verdict[round.verdict]}
        </Tag>
      </td>
    </tr>
  );
}

function togglePick(current: readonly RoundId[], round: RoundId): readonly RoundId[] {
  if (current.includes(round)) {
    return current.filter((picked) => picked !== round);
  }

  return [...current, round].slice(-ROUNDS_COMPARED);
}

function toneOf(round: RoundSummary): TagTone {
  switch (round.verdict) {
    case 'pass':
      return 'ok';
    case 'fail':
      return 'broken';
    case 'inconclusive':
      return 'running';
    case null:
      return 'neutral';
  }
}
