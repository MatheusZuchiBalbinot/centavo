import type { RoundEvent } from '../api/replay';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';
import { isAttackEvent } from './phase';

export type FeedTone = 'attack' | 'heal' | 'other';

// A feed item says what happened and what should follow from it.
export type FeedItem = {
  readonly key: string;
  readonly tMs: number;
  readonly tone: FeedTone;
  // number matches the event's marker on the timeline; cure events have none.
  readonly number: number | null;
  readonly what: string;
  readonly expected: string;
};

// feedOf tells every event of the round, newest first, numbering the attacks
// in the order they happened.
export function feedOf(events: readonly RoundEvent[]): readonly FeedItem[] {
  let attackNumber = 0;
  const items: FeedItem[] = [];

  for (const [index, event] of events.entries()) {
    const isAttack = isAttackEvent(event);

    if (isAttack) {
      attackNumber += 1;
    }

    items.push(itemOf(event, index, isAttack ? attackNumber : null));
  }

  return items.reverse();
}

// describeEvent is the sentence of one event, also used by its toast.
export function describeEvent(event: RoundEvent): { what: string; expected: string } {
  const copy = messages.feed.events[event.kind];
  const values = {
    target: event.target || messages.feed.someone,
    detail: event.detail,
    duration: event.duration,
  };
  const expected = copy.expect === '' ? '' : format(messages.feed.expected, { text: copy.expect });

  return { what: format(copy.what, values), expected };
}

function itemOf(event: RoundEvent, index: number, number: number | null): FeedItem {
  const { what, expected } = describeEvent(event);

  return { key: `${event.tMs}-${index}`, tMs: event.tMs, tone: toneOf(event), number, what, expected };
}

function toneOf(event: RoundEvent): FeedTone {
  if (event.kind === 'other') {
    return 'other';
  }

  return isAttackEvent(event) ? 'attack' : 'heal';
}
