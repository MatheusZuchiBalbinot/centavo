import { useEffect, useRef } from 'react';

import type { RoundEvent } from '../../../api/replay';
import { describeEvent } from '../../../domain/feed';
import { isAttackEvent } from '../../../domain/phase';
import { useToasts } from '../../../hooks/toastContext';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';

// At most one toast in this window; attacks that land meanwhile are told
// together in the next one, so a burst never floods the screen or a screen
// reader.
const TOAST_WINDOW_MS = 4_000;

// useAttackToasts announces the attacks that arrive after the page opened.
// events is the whole list; newEvents is how many of its last items are new.
export function useAttackToasts(events: readonly RoundEvent[], newEvents: number): void {
  const { push } = useToasts();
  const announced = useRef(0);
  const pending = useRef<RoundEvent[]>([]);
  const lastToastAt = useRef(0);

  useEffect(() => {
    // A new snapshot, after a reconnection, counts from zero again.
    if (newEvents < announced.current) {
      announced.current = 0;
    }

    const fresh = events.slice(events.length - newEvents + announced.current);

    announced.current = newEvents;
    pending.current.push(...fresh.filter(isAttackEvent));

    if (pending.current.length === 0) {
      return;
    }

    function flush(): void {
      const burst = pending.current;
      const last = burst.at(-1);

      pending.current = [];
      lastToastAt.current = Date.now();

      if (last === undefined) {
        return;
      }

      const { what, expected } = describeEvent(last);

      if (burst.length === 1) {
        push({ tone: 'attack', title: what, detail: expected });

        return;
      }

      push({
        tone: 'attack',
        title: format(messages.toasts.grouped, { count: burst.length }),
        detail: format(messages.toasts.groupedDetail, { what }),
      });
    }

    const wait = lastToastAt.current + TOAST_WINDOW_MS - Date.now();

    if (wait <= 0) {
      flush();

      return;
    }

    const timer = window.setTimeout(flush, wait);

    return () => {
      window.clearTimeout(timer);
    };
  }, [events, newEvents, push]);
}
