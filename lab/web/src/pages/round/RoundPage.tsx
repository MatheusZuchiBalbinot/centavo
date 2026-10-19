import { useParams } from 'react-router';

import { isRoundId } from '../../api/ids';
import type { RoundId } from '../../api/ids';
import type { Replay } from '../../api/replay';
import { expectationOf, headlineOf } from '../../domain/headline';
import { phaseOf } from '../../domain/phase';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useNow } from '../../hooks/useNow';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import NotFoundPage from '../not-found/NotFoundPage';
import EventFeed from './components/EventFeed';
import Guarantees from './components/Guarantees';
import MoneyBoard from './components/MoneyBoard';
import PhaseStepper from './components/PhaseStepper';
import ResultAside from './components/ResultAside';
import RoundActions from './components/RoundActions';
import Timeline from './components/Timeline';
import styles from './components/RoundPage.module.css';
import { useAttackToasts } from './hooks/useAttackToasts';
import { useRoundStream } from './hooks/useRoundStream';

const CLOCK_TICK_MS = 1_000;
const MS_PER_SECOND = 1_000;

export default function RoundPage(): React.JSX.Element {
  const { round } = useParams();

  if (!isRoundId(round)) {
    return <NotFoundPage />;
  }

  return <FollowedRound round={round} />;
}

function FollowedRound({ round }: { round: RoundId }): React.JSX.Element {
  const { state, isConnected } = useRoundStream(round);
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

  return (
    <article className={styles.round} data-verdict={isFinished ? (replay.verdict ?? 'none') : 'running'}>
      <header className={styles.top}>
        <div>
          <p className={styles.roundName}>{format(messages.round.name, { round, attack: replay.scenario })}</p>
          <h1 id={PAGE_HEADING_ID} className={styles.headline} tabIndex={-1}>
            {headline}
          </h1>
          {replay.description === '' ? null : <p className={styles.description}>{replay.description}</p>}
          <p className={styles.expectation}>{expectationOf(replay.defenses)}</p>
          {replay.isStopped ? <p className={styles.stopped}>{messages.round.stopped}</p> : null}
          {isConnected ? null : (
            <p className={styles.reconnecting} role="status">
              {messages.round.reconnecting}
            </p>
          )}
        </div>
        <RoundActions round={round} isFinished={isFinished} />
      </header>
      <PhaseStepper phase={phase} elapsedSeconds={elapsedSeconds} durationSeconds={replay.durationSeconds} />
      <div className={styles.grid}>
        <div className={styles.main}>
          <MoneyBoard replay={replay} reading={reading} isFinished={isFinished} />
          <section className={styles.chartSheet} aria-labelledby="chart-title">
            <h2 id="chart-title" className={styles.sectionTitle}>
              {messages.round.chartTitle}
            </h2>
            <Timeline replay={replay} nowSeconds={nowSeconds} />
          </section>
          <Guarantees checks={replay.checks} reading={reading} isFinished={isFinished} />
        </div>
        {isFinished ? <ResultAside replay={replay} /> : <EventFeed events={replay.events} isCalm={isCalm(replay)} />}
      </div>
      {isFinished ? <EventFeed events={replay.events} isCalm={isCalm(replay)} /> : null}
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

function isCalm(replay: Replay): boolean {
  return replay.scenario === 'smoke' || replay.scenario === 'concurrency-hotspot';
}
