import { useEffect, useState } from 'react';
import { useParams, useSearchParams } from 'react-router';

import { isRoundId } from '../../api/ids';
import type { RoundId } from '../../api/ids';
import type { Replay } from '../../api/replay';
import Arena from '../../components/arena/Arena';
import { betOutcome, isBet, isBetBook, withBet } from '../../domain/bets';
import type { Bet } from '../../domain/bets';
import { headlineOf } from '../../domain/headline';
import type { AttackRange } from '../../domain/highlight';
import { phaseOf } from '../../domain/phase';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useNow } from '../../hooks/useNow';
import { useViewMode } from '../../hooks/viewModeContext';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { QUERY } from '../../routes';
import { readStored, STORAGE_KEYS, writeStored } from '../../storage';
import NotFoundPage from '../not-found/NotFoundPage';
import EventFeed from './components/EventFeed';
import Guarantees from './components/Guarantees';
import ResultAside from './components/ResultAside';
import RoundChart from './components/RoundChart';
import RoundHero from './components/RoundHero';
import styles from './components/RoundPage.module.css';
import RoundActions from './components/RoundActions';
import RoundTiles from './components/RoundTiles';
import { useAttackToasts } from './hooks/useAttackToasts';
import { useRoundStream } from './hooks/useRoundStream';

const CLOCK_TICK_MS = 1_000;
const MS_PER_SECOND = 1_000;
// The arena's query value: open, or closed on purpose in Presentation mode.
const ARENA_OPEN = '1';
const ARENA_CLOSED = '0';
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
  const [searchParams, setSearchParams] = useSearchParams();
  const isPresenting = useViewMode().mode === 'presentation';
  const arenaParam = searchParams.get(QUERY.arena);
  const isArenaOpen = arenaParam === ARENA_OPEN || (isPresenting && arenaParam !== ARENA_CLOSED);
  const isSurprise = searchParams.get(QUERY.surprise) === ARENA_OPEN;
  const betParam = searchParams.get(QUERY.bet);
  const bet = isBet(betParam) ? betParam : null;

  useEffect(() => {
    if (bet === null || !isFinished) {
      return;
    }

    recordBet(round, bet);
  }, [bet, isFinished, round]);

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
  const eyebrow = format(arenaEyebrowOf(isFinished, isSurprise), { round });

  function setArena(value: string): void {
    const next = new URLSearchParams(searchParams);
    next.set(QUERY.arena, value);
    setSearchParams(next, { replace: true });
  }

  function handleOpenArena(): void {
    setArena(ARENA_OPEN);
  }

  function handleCloseArena(): void {
    setArena(ARENA_CLOSED);
  }

  return (
    <article className={styles.round}>
      <RoundHero
        round={round}
        replay={replay}
        phase={phase}
        headline={headline}
        elapsedSeconds={elapsedSeconds}
        isConnected={isConnected}
        onOpenArena={handleOpenArena}
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
      {isArenaOpen ? (
        <Arena
          eyebrow={eyebrow}
          title={headline}
          replay={replay}
          reading={reading}
          phase={phase}
          nowMs={arenaNowOf(replay, now, isFinished)}
          ageSeconds={ageSeconds}
          wallMs={now}
          betLine={bet === null ? null : betLineOf(bet, replay.verdict)}
          actions={<RoundActions round={round} isFinished={isFinished} />}
          onClose={handleCloseArena}
        />
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

// arenaNowOf is the moment the arena shows: now while the round runs, past
// the attack into the cure, and the very end once it is over.
function arenaNowOf(replay: Replay, now: number, isFinished: boolean): number {
  const lastSecondMs = (replay.seconds.at(-1)?.t ?? 0) * MS_PER_SECOND;
  const lastEventMs = replay.events.at(-1)?.tMs ?? 0;
  const endMs = Math.max(lastSecondMs, lastEventMs);

  if (isFinished || replay.startedAt === null) {
    return endMs;
  }

  return Math.max(endMs, now - replay.startedAt.getTime());
}

function arenaEyebrowOf(isFinished: boolean, isSurprise: boolean): string {
  if (isFinished) {
    return messages.arena.eyebrowDone;
  }

  return isSurprise ? messages.arena.eyebrowSurprise : messages.arena.eyebrow;
}

// recordBet keeps the bet in this browser, for the lab's scoreboard.
function recordBet(round: RoundId, bet: Bet): void {
  const book = readStored(STORAGE_KEYS.bets, isBetBook) ?? {};

  writeStored(STORAGE_KEYS.bets, withBet(book, round, bet));
}

const BET_LINES = {
  holds: { right: messages.arena.bet.holdsRight, wrong: messages.arena.bet.holdsWrong },
  breaks: { right: messages.arena.bet.breaksRight, wrong: messages.arena.bet.breaksWrong },
} as const;

function betLineOf(bet: Bet, verdict: Replay['verdict']): string | null {
  const outcome = betOutcome(bet, verdict);

  return outcome === null ? null : BET_LINES[bet][outcome];
}
