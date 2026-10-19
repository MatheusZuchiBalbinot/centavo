import { GitCompare } from 'lucide-react';
import { useState } from 'react';

import type { RoundId } from '../../api/ids';
import RoundsTable from '../../components/RoundsTable';
import type { RoundPicking } from '../../components/RoundsTable';
import { Button, LinkButton } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { comparePath } from '../../routes';
import { useRoundPages } from './useRoundPages';
import type { RoundPages } from './useRoundPages';

// Comparing takes exactly two rounds.
const ROUNDS_COMPARED = 2;

export default function RoundsPage(): React.JSX.Element {
  useDocumentTitle(messages.rounds.title);

  const pages = useRoundPages();
  const [picked, setPicked] = useState<readonly RoundId[]>([]);

  function handleToggle(round: RoundId): void {
    setPicked((current) => togglePick(current, round));
  }

  const picking: RoundPicking = { picked, limit: ROUNDS_COMPARED, onToggle: handleToggle };

  return (
    <>
      <header className="page-header">
        <div>
          <h1 id={PAGE_HEADING_ID} className="page-title" tabIndex={-1}>
            {messages.rounds.headline}
          </h1>
          <p className="page-desc">{messages.rounds.lead}</p>
        </div>
        <div className="page-actions">
          <CompareButton picked={picked} />
        </div>
      </header>
      <Card
        title={messages.rounds.listTitle}
        titleId="rounds-title"
        bodyClassName=""
        aside={<span className="card-note">{format(messages.rounds.pickedNote, { count: picked.length })}</span>}
      >
        <RoundsBody pages={pages} picking={picking} />
      </Card>
    </>
  );
}

function CompareButton({ picked }: { picked: readonly RoundId[] }): React.JSX.Element {
  const [left, right] = picked;
  const icon = <GitCompare size={16} aria-hidden="true" />;

  if (left === undefined || right === undefined) {
    return (
      <Button variant="primary" disabled>
        {icon}
        {messages.rounds.compare}
      </Button>
    );
  }

  return (
    <LinkButton to={comparePath(left, right)} variant="primary">
      {icon}
      {messages.rounds.compare}
    </LinkButton>
  );
}

function RoundsBody({ pages, picking }: { pages: RoundPages; picking: RoundPicking }): React.JSX.Element {
  switch (pages.kind) {
    case 'loading':
      return <p className="card-body muted">{messages.rounds.loading}</p>;
    case 'failed':
      return (
        <p className="card-body" role="alert">
          {messages.rounds.failed}
        </p>
      );
    case 'ready':
      return pages.rounds.length === 0 ? (
        <p className="card-body muted">{messages.rounds.empty}</p>
      ) : (
        <>
          <RoundsTable rounds={pages.rounds} picking={picking} />
          {pages.hasMore ? (
            <div className="card-body">
              <Button onClick={pages.loadMore} disabled={pages.isLoadingMore}>
                {messages.rounds.more}
              </Button>
            </div>
          ) : null}
        </>
      );
  }
}

function togglePick(current: readonly RoundId[], round: RoundId): readonly RoundId[] {
  if (current.includes(round)) {
    return current.filter((picked) => picked !== round);
  }

  return [...current, round].slice(-ROUNDS_COMPARED);
}
