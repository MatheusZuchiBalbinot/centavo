import type { Replay } from '../api/replay';
import { phaseOf } from './phase';
import type { Phase } from './phase';

const MS_PER_SECOND = 1_000;
const SECONDS_PER_MINUTE = 60;
const CLOCK_PATTERN = /^(\d{1,3}):([0-5]\d)$/;

// endOfMs is when a recorded round ends: its last second or its last event,
// whichever comes later.
export function endOfMs(replay: Replay): number {
  const lastSecondMs = ((replay.seconds.at(-1)?.t ?? -1) + 1) * MS_PER_SECOND;
  const lastEventMs = replay.events.at(-1)?.tMs ?? 0;

  return Math.max(lastSecondMs, lastEventMs);
}

// replayAt is the round as it stood at a moment: what had happened by then,
// and the checker's word only once the round is over.
export function replayAt(replay: Replay, tMs: number): Replay {
  const isOver = tMs >= endOfMs(replay);

  if (isOver) {
    return replay;
  }

  const seconds = replay.seconds.filter((second) => second.t * MS_PER_SECOND <= tMs);
  const events = replay.events.filter((event) => event.tMs <= tMs);

  return { ...replay, seconds, events, verdict: null, checks: [] };
}

export function playbackPhase(replay: Replay, tMs: number): Phase {
  return phaseOf(replayAt(replay, tMs), tMs >= endOfMs(replay));
}

// parseClock reads a moment written as m:ss, as the links carry it; null
// when the text is not one.
export function parseClock(text: string | null): number | null {
  const match = CLOCK_PATTERN.exec(text ?? '');

  if (match === null) {
    return null;
  }

  const minutes = Number(match[1]);
  const seconds = Number(match[2]);

  return (minutes * SECONDS_PER_MINUTE + seconds) * MS_PER_SECOND;
}
