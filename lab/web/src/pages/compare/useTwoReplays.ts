import { useEffect, useState } from 'react';

import type { RoundId } from '../../api/ids';
import { fetchReplay } from '../../api/replay';
import type { Replay } from '../../api/replay';
import type { ApiResult } from '../../api/client';

export type TwoReplays =
  | { readonly kind: 'loading' }
  // missing names the rounds that do not exist; failed is a lab that did
  // not answer.
  | { readonly kind: 'missing'; readonly rounds: readonly RoundId[] }
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

      if (leftResult.ok && rightResult.ok) {
        setReading({ kind: 'ready', left: leftResult.data, right: rightResult.data });

        return;
      }

      const loaded: readonly LoadedRound[] = [
        { round: left, result: leftResult },
        { round: right, result: rightResult },
      ];
      const missing = loaded.filter(isMissing).map((entry) => entry.round);

      setReading(missing.length > 0 ? { kind: 'missing', rounds: missing } : { kind: 'failed' });
    }

    void load();

    return () => {
      controller.abort();
    };
  }, [left, right]);

  return reading;
}

type LoadedRound = { readonly round: RoundId; readonly result: ApiResult<Replay> };

function isMissing({ result }: LoadedRound): boolean {
  return !result.ok && result.error.code === 'not_found';
}
