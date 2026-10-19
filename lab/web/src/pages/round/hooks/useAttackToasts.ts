import { useEffect, useRef } from 'react';

import type { RoundEvent } from '../../../api/replay';
import { describeEvent } from '../../../domain/feed';
import { isAttackEvent, isDefenseEvent } from '../../../domain/phase';
import { useToasts } from '../../../hooks/toastContext';
import type { ToastInput } from '../../../hooks/toastContext';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';

// At most one toast of each kind in this window; events that land meanwhile
// are told together in the next one, so a burst never floods the screen or
// a screen reader.
const TOAST_WINDOW_MS = 4_000;

type Kind = 'attack' | 'defense';

const KINDS: readonly Kind[] = ['attack', 'defense'];

// useAttackToasts announces the attacks and the defenses that arrive after
// the page opened. events is the whole list; newEvents is how many of its
// last items are new.
export function useAttackToasts(events: readonly RoundEvent[], newEvents: number): void {
  const { push } = useToasts();
  const announced = useRef(0);
  const pending = useRef<Record<Kind, RoundEvent[]>>({ attack: [], defense: [] });
  const lastToastAt = useRef<Record<Kind, number>>({ attack: 0, defense: 0 });

  useEffect(() => {
    // A new snapshot, after a reconnection, counts from zero again.
    if (newEvents < announced.current) {
      announced.current = 0;
    }

    const fresh = events.slice(events.length - newEvents + announced.current);

    announced.current = newEvents;
    pending.current.attack.push(...fresh.filter(isAttackEvent));
    pending.current.defense.push(...fresh.filter(isDefenseEvent));

    function flush(kind: Kind): void {
      const burst = pending.current[kind];

      pending.current[kind] = [];
      lastToastAt.current[kind] = Date.now();

      const toast = toastOf(kind, burst);

      if (toast !== null) {
        push(toast);
      }
    }

    function hasPending(kind: Kind): boolean {
      return pending.current[kind].length > 0;
    }

    function schedule(kind: Kind): number {
      const wait = lastToastAt.current[kind] + TOAST_WINDOW_MS - Date.now();

      return window.setTimeout(() => flush(kind), Math.max(0, wait));
    }

    const timers = KINDS.filter(hasPending).map(schedule);

    return () => {
      for (const timer of timers) {
        window.clearTimeout(timer);
      }
    };
  }, [events, newEvents, push]);
}

function toastOf(kind: Kind, burst: readonly RoundEvent[]): ToastInput | null {
  const last = burst.at(-1);

  if (last === undefined) {
    return null;
  }

  const { what, expected } = describeEvent(last);
  const tone = kind === 'attack' ? 'attack' : 'info';

  if (burst.length === 1) {
    return expected === '' ? { tone, title: what } : { tone, title: what, detail: expected };
  }

  const grouped = kind === 'attack' ? messages.toasts.grouped : messages.toasts.groupedDefenses;

  return {
    tone,
    title: format(grouped, { count: burst.length }),
    detail: format(messages.toasts.groupedDetail, { what }),
  };
}
