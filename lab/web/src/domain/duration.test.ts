import { describe, expect, it } from 'vitest';

import { formatClock, formatDuration } from './duration';

describe('formatDuration', () => {
  it.each([
    [0, '0 s'],
    [45, '45 s'],
    [180, '3 min'],
    [90, '1 min 30 s'],
    [3_900, '1 h 5 min'],
    [-5, '0 s'],
  ])('%i seconds read as %s', (seconds, text) => {
    expect(formatDuration(seconds)).toBe(text);
  });
});

describe('formatClock', () => {
  it.each([
    [0, '0:00'],
    [65, '1:05'],
    [600.9, '10:00'],
  ])('%f seconds sit at %s', (seconds, clock) => {
    expect(formatClock(seconds)).toBe(clock);
  });
});
