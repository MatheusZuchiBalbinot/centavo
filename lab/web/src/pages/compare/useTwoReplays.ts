import { useEffect, useState } from 'react';

import type { RoundId } from '../../api/ids';
import { fetchReplay } from '../../api/replay';
import type { Replay } from '../../api/replay';

export type TwoReplays =
  | { readonly kind: 'loading' }
  | { readonly kind: 'failed' }
  | { readonly kind: 'ready'; readonly left: Replay; readonly right: Replay };

// useTwoReplays reads two finished rounds for the side by side view.
export function useTwoReplays(left: RoundId, right: RoundId): TwoReplays {
  const [reading, setReading] = useState<TwoReplays>({ kind: 'loading' });

  useEffect(() => {
    const controller = new AbortController();

    async function load(): Promise<void> {
      const [leftResult, rightResult] = await Promise.all([
        fetchReplay(left, controller.signal),
        fetchReplay(right, controller.signal),
      ]);

      if (controller.signal.aborted) {
        return;
      }

      const isLoaded = leftResult.ok && rightResult.ok;

      setReading(isLoaded ? { kind: 'ready', left: leftResult.data, right: rightResult.data } : { kind: 'failed' });
    }

    void load();

    return () => {
      controller.abort();
    };
  }, [left, right]);

  return reading;
}
