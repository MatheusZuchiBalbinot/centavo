import { useState } from 'react';
import { useParams } from 'react-router';

import { isRoundId } from '../../api/ids';
import type { RoundId } from '../../api/ids';
import type { Replay } from '../../api/replay';
import { headlineOf } from '../../domain/headline';
import type { AttackRange } from '../../domain/highlight';
import { phaseOf } from '../../domain/phase';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useNow } from '../../hooks/useNow';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import NotFoundPage from '../not-found/NotFoundPage';
import EventFeed from './components/EventFeed';
import Guarantees from './components/Guarantees';
import ResultAside from './components/ResultAside';
import RoundChart from './components/RoundChart';
import RoundHero from './components/RoundHero';
import styles from './components/RoundPage.module.css';
import RoundTiles from './components/RoundTiles';
import { useAttackToasts } from './hooks/useAttackToasts';
import { useRoundStream } from './hooks/useRoundStream';

const CLOCK_TICK_MS = 1_000;
const MS_PER_SECOND = 1_000;
const CALM_SCENARIOS: ReadonlySet<string> = new Set(['smoke', 'concurrency-hotspot']);

export default function RoundPage(): React.JSX.Element {
  const { round } = useParams();

  if (!isRoundId(round)) {
    return <NotFoundPage />;
  }

  return <FollowedRound round={round} />;
}

function FollowedRound({ round }: { round: RoundId }): React.JSX.Element {
  const { state, isConnected, lastMessageAt } = useRoundStream(round);
  const [highlight, setHighlight] = useState<AttackRange | null>(null);
  const isFinished = state.kind === 'finished';
  const now = useNow(CLOCK_TICK_MS, !isFinished);
  const events = state.kind === 'connecting' ? [] : state.replay.events;
  const newEvents = state.kind === 'connecting' ? 0 : state.newEvents;

  useAttackToasts(events, newEvents);
  useDocumentTitle(format(isFinished ? messages.round.titleDone : messages.round.titleLive, { round }));

  if (state.kind === 'connecting') {
    return (
      <h1 id={PAGE_HEADING_ID} className={styles.connecting} tabIndex={-1}>
        {messages.round.connecting}
      </h1>
    );
  }

  const { replay, reading } = state;
  const phase = phaseOf(replay, isFinished);
  const elapsedSeconds = elapsedOf(replay, now);
  const nowSeconds = phase === 'attack' ? elapsedSeconds : null;
  const headline = headlineOf({ replay, phase, elapsedSeconds, reading });
  const ageSeconds = lastMessageAt === null ? null : Math.max(0, Math.round((now - lastMessageAt) / MS_PER_SECOND));
  const isCalm = CALM_SCENARIOS.has(replay.scenario);

  return (
    <article className={styles.round}>
      <RoundHero
        round={round}
        replay={replay}
        phase={phase}
        headline={headline}
        elapsedSeconds={elapsedSeconds}
        isConnected={isConnected}
      />
      <RoundTiles replay={replay} reading={reading} isFinished={isFinished} />
      <div className={styles.grid}>
        <div className={styles.main}>
          <RoundChart
            replay={replay}
            isLive={!isFinished}
            nowSeconds={nowSeconds}
            ageSeconds={ageSeconds}
            highlight={highlight}
            onHighlight={setHighlight}
          />
          <Guarantees checks={replay.checks} reading={reading} isFinished={isFinished} />
        </div>
        {isFinished ? (
          <ResultAside replay={replay} />
        ) : (
          <EventFeed events={replay.events} isCalm={isCalm} highlight={highlight} onHighlight={setHighlight} />
        )}
      </div>
      {isFinished ? (
        <EventFeed events={replay.events} isCalm={isCalm} highlight={highlight} onHighlight={setHighlight} />
      ) : null}
    </article>
  );
}

// elapsedOf is how long the attack has been running, from the round's own
// start; it stops growing at the planned duration.
function elapsedOf(replay: Replay, now: number): number {
  if (replay.startedAt === null) {
    return replay.seconds.length;
  }

  const elapsed = (now - replay.startedAt.getTime()) / MS_PER_SECOND;

  return Math.max(0, Math.min(elapsed, replay.durationSeconds || elapsed));
}
