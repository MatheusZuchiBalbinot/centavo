import { useEffect, useState } from 'react';

import { fetchRounds } from '../../../api/rounds';
import type { RoundSummary } from '../../../api/rounds';

// The home page sums up this many rounds and shows the newest few.
const ROUNDS_READ = 100;

type RoundsReading =
  | { readonly kind: 'loading' }
  | { readonly kind: 'ready'; readonly rounds: readonly RoundSummary[] }
  | { readonly kind: 'failed' };

// useRecentRounds reads the newest finished rounds, again each time
// refreshKey changes: the page passes the lab status, so a round that just
// ended shows up.
export function useRecentRounds(refreshKey: string): RoundsReading {
  const [reading, setReading] = useState<RoundsReading>({ kind: 'loading' });

  useEffect(() => {
    const controller = new AbortController();

    async function load(): Promise<void> {
      const result = await fetchRounds({ limit: ROUNDS_READ }, controller.signal);

      if (controller.signal.aborted) {
        return;
      }

      setReading(result.ok ? { kind: 'ready', rounds: result.data.rounds } : { kind: 'failed' });
    }

    void load();

    return () => {
      controller.abort();
    };
  }, [refreshKey]);

  return reading;
}
