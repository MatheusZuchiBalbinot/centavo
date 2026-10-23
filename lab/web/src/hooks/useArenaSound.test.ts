import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { playCue } from '../audio/cues';
import type { Verdict } from '../api/rounds';
import { useArenaSound } from './useArenaSound';

vi.mock('../audio/cues', () => ({ playCue: vi.fn(), unlockAudio: vi.fn() }));

type Props = { attacks: number; verdict: Verdict | null };

afterEach(() => {
  window.localStorage.clear();
  vi.mocked(playCue).mockClear();
});

describe('the arena sound', () => {
  it('stays silent until turned on, and remembers the choice', () => {
    const initialProps: Props = { attacks: 0, verdict: null };
    const { result, rerender } = renderHook((props: Props) => useArenaSound(props), { initialProps });

    rerender({ attacks: 1, verdict: null });

    expect(playCue).not.toHaveBeenCalled();

    act(() => {
      result.current.toggle();
    });

    expect(result.current.isOn).toBe(true);
    expect(window.localStorage.getItem('centavo.sound')).toBe('true');
  });

  it('plays a cue per new attack and one for the verdict', () => {
    window.localStorage.setItem('centavo.sound', 'true');
    const initialProps: Props = { attacks: 0, verdict: null };
    const { rerender } = renderHook((props: Props) => useArenaSound(props), { initialProps });

    rerender({ attacks: 1, verdict: null });
    rerender({ attacks: 1, verdict: 'fail' });

    expect(vi.mocked(playCue).mock.calls).toEqual([['attack'], ['fail']]);
  });
});
