import { useEffect, useState } from 'react';

import { fetchAttackMenu } from '../../../api/attacks';
import type { AttackMenu } from '../../../api/attacks';

export type MenuReading =
  { readonly kind: 'loading' } | { readonly kind: 'ready'; readonly menu: AttackMenu } | { readonly kind: 'failed' };

// useAttackMenu reads the lab's attack menu once.
export function useAttackMenu(): MenuReading {
  const [reading, setReading] = useState<MenuReading>({ kind: 'loading' });

  useEffect(() => {
    const controller = new AbortController();

    async function load(): Promise<void> {
      const result = await fetchAttackMenu(controller.signal);

      if (controller.signal.aborted) {
        return;
      }

      setReading(result.ok ? { kind: 'ready', menu: result.data } : { kind: 'failed' });
    }

    void load();

    return () => {
      controller.abort();
    };
  }, []);

  return reading;
}
