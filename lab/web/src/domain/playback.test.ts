import { describe, expect, it } from 'vitest';

import { aReplay, aSecond, anEvent } from '../test/fixtures';
import { endOfMs, parseClock, playbackPhase, replayAt } from './playback';

const replay = aReplay({
  seconds: [aSecond(0), aSecond(1), aSecond(2), aSecond(3)],
  events: [anEvent(1_500, 'kill'), anEvent(2_000, 'heal_start'), anEvent(3_000, 'heal_complete')],
  verdict: 'pass',
});

describe('playback', () => {
  it('shows only what had happened by the moment, and the verdict at the end', () => {
    const early = replayAt(replay, 1_600);

    expect(early.events).toHaveLength(1);
    expect(early.seconds).toHaveLength(2);
    expect(early.verdict).toBeNull();
    expect(replayAt(replay, endOfMs(replay)).verdict).toBe('pass');
  });

  it('walks the phases as the recording plays', () => {
    expect(playbackPhase(replay, 1_000)).toBe('attack');
    expect(playbackPhase(replay, 2_500)).toBe('heal');
    expect(playbackPhase(replay, 3_500)).toBe('check');
    expect(playbackPhase(replay, 4_000)).toBe('verdict');
  });

  it('reads a moment from a link, and nothing from anything else', () => {
    expect(parseClock('1:24')).toBe(84_000);
    expect(parseClock('0:00')).toBe(0);
    expect(parseClock('1:99')).toBeNull();
    expect(parseClock('abc')).toBeNull();
    expect(parseClock(null)).toBeNull();
  });
});
