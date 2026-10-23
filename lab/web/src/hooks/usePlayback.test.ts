import { describe, expect, it } from 'vitest';

import { advance } from './usePlayback';

const playing = { positionMs: 0, speed: 30, isPlaying: true } as const;

describe('the playback clock', () => {
  it('runs at its speed and stops at the end', () => {
    const bounds = { endMs: 10_000, isLooping: false, holdMs: 0 };

    expect(advance(playing, 100, bounds).positionMs).toBe(3_000);
    expect(advance({ ...playing, positionMs: 9_000 }, 100, bounds)).toEqual({
      ...playing,
      positionMs: 10_000,
      isPlaying: false,
    });
  });

  it('rests on the end for the hold at real speed, then loops', () => {
    const bounds = { endMs: 10_000, isLooping: true, holdMs: 1_000 };
    const atEnd = advance({ ...playing, positionMs: 9_000 }, 100, bounds);

    expect(atEnd.positionMs).toBe(10_000);
    expect(advance({ ...playing, positionMs: 10_500 }, 100, bounds).positionMs).toBe(10_600);
    expect(advance({ ...playing, positionMs: 10_950 }, 100, bounds).positionMs).toBe(0);
  });
});
