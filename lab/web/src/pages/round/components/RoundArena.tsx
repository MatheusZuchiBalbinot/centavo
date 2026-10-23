import { useEffect } from 'react';

import type { RoundId } from '../../../api/ids';
import type { AuditorReading, Replay } from '../../../api/replay';
import Arena from '../../../components/arena/Arena';
import ReplayArena from '../../../components/arena/ReplayArena';
import { betOutcome, isBetBook, withBet } from '../../../domain/bets';
import type { Bet } from '../../../domain/bets';
import type { Phase } from '../../../domain/phase';
import { endOfMs } from '../../../domain/playback';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import { readStored, STORAGE_KEYS, writeStored } from '../../../storage';
import type { ArenaQuery } from '../hooks/useArenaQuery';
import RoundActions from './RoundActions';

// A replay asked for by its link plays at this speed.
const REPLAY_SPEED = 10;

const BET_LINES = {
  holds: { right: messages.arena.bet.holdsRight, wrong: messages.arena.bet.holdsWrong },
  breaks: { right: messages.arena.bet.breaksRight, wrong: messages.arena.bet.breaksWrong },
} as const;

type RoundArenaProps = {
  readonly round: RoundId;
  readonly replay: Replay;
  readonly reading: AuditorReading | null;
  readonly phase: Phase;
  readonly headline: string;
  readonly now: number;
  readonly ageSeconds: number | null;
  readonly query: ArenaQuery;
};

// RoundArena is the round page's arena: the round as it happens, or, once
// it is over and a moment is asked for, its replay.
export default function RoundArena(props: RoundArenaProps): React.JSX.Element | null {
  const { round, replay, reading, phase, headline, now, ageSeconds, query } = props;
  const isFinished = phase === 'verdict';
  const { bet } = query;

  useEffect(() => {
    if (bet === null || !isFinished) {
      return;
    }

    recordBet(round, bet);
  }, [bet, isFinished, round]);

  if (!query.isOpen) {
    return null;
  }

  const actions = <RoundActions round={round} isFinished={isFinished} />;
  const isReplay = isFinished && query.replayFromMs !== null;

  if (isReplay) {
    return (
      <ReplayArena
        eyebrow={format(messages.arena.eyebrowReplay, { round })}
        replay={replay}
        startMs={query.replayFromMs}
        speed={REPLAY_SPEED}
        isLooping={false}
        holdMs={0}
        wallMs={now}
        actions={actions}
        onPause={query.keepMoment}
        onClose={query.close}
      />
    );
  }

  return (
    <Arena
      eyebrow={format(eyebrowOf(isFinished, query.isSurprise), { round })}
      title={headline}
      replay={replay}
      reading={reading}
      phase={phase}
      nowMs={arenaNowOf(replay, now, isFinished)}
      ageSeconds={ageSeconds}
      wallMs={now}
      betLine={bet === null ? null : betLineOf(bet, replay.verdict)}
      actions={actions}
      isLive
      onWatchAgain={isFinished ? query.watchAgain : undefined}
      onClose={query.close}
    />
  );
}

// arenaNowOf is the moment the arena shows: now while the round runs, past
// the attack into the cure, and the very end once it is over.
function arenaNowOf(replay: Replay, now: number, isFinished: boolean): number {
  const endMs = endOfMs(replay);

  if (isFinished || replay.startedAt === null) {
    return endMs;
  }

  return Math.max(endMs, now - replay.startedAt.getTime());
}

function eyebrowOf(isFinished: boolean, isSurprise: boolean): string {
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

function betLineOf(bet: Bet, verdict: Replay['verdict']): string | null {
  const outcome = betOutcome(bet, verdict);

  return outcome === null ? null : BET_LINES[bet][outcome];
}
