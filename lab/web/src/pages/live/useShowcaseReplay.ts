import { useEffect, useState } from 'react';

import { fetchReplay } from '../../api/replay';
import type { Replay } from '../../api/replay';
import { fetchRounds } from '../../api/rounds';
import type { RoundSummary } from '../../api/rounds';

// The showcase looks this far back for a round worth replaying.
const ROUNDS_READ = 10;

export type ShowcaseReading =
  { readonly kind: 'loading' } | { readonly kind: 'ready'; readonly replay: Replay } | { readonly kind: 'none' };

// useShowcaseReplay loads the newest round that reached a verdict, for the
// showcase to replay; a stopped or inconclusive round tells too little.
export function useShowcaseReplay(isWanted: boolean): ShowcaseReading {
  const [reading, setReading] = useState<ShowcaseReading>({ kind: 'loading' });

  useEffect(() => {
    if (!isWanted) {
      return;
    }

    const controller = new AbortController();

    async function load(): Promise<void> {
      const rounds = await fetchRounds({ limit: ROUNDS_READ }, controller.signal);
      const settled = rounds.ok ? rounds.data.rounds.find(isSettled) : undefined;

      if (settled === undefined) {
        setReading({ kind: 'none' });

        return;
      }

      const replay = await fetchReplay(settled.round, controller.signal);

      if (controller.signal.aborted) {
        return;
      }

      setReading(replay.ok ? { kind: 'ready', replay: replay.data } : { kind: 'none' });
    }

    void load();

    return () => {
      controller.abort();
    };
  }, [isWanted]);

  return reading;
}

function isSettled(round: RoundSummary): boolean {
  return round.verdict === 'pass' || round.verdict === 'fail';
}
