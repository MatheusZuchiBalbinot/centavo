import { useEffect, useState } from 'react';

import { fetchLabState } from '../api/lab';
import type { LabState } from '../api/lab';

// How often the bar asks the lab what it is doing.
const LAB_POLL_MS = 3_000;

export type LabReading =
  { readonly kind: 'loading' } | { readonly kind: 'ready'; readonly state: LabState } | { readonly kind: 'offline' };

// useLabState polls the lab's state for as long as the component lives.
export function useLabState(): LabReading {
  const [reading, setReading] = useState<LabReading>({ kind: 'loading' });

  useEffect(() => {
    const controller = new AbortController();

    async function poll(): Promise<void> {
      const result = await fetchLabState(controller.signal);

      if (controller.signal.aborted) {
        return;
      }

      setReading(result.ok ? { kind: 'ready', state: result.data } : { kind: 'offline' });
    }

    void poll();
    const timer = window.setInterval(() => void poll(), LAB_POLL_MS);

    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, []);

  return reading;
}
