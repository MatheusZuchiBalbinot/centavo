import { useRef, useState } from 'react';

import type { RoundEvent } from '../../../api/replay';
import { Card } from '../../../components/ui/Card';
import { Glossed } from '../../../components/ui/Glossed';
import { formatClock } from '../../../domain/duration';
import { feedOf } from '../../../domain/feed';
import type { FeedItem, FeedTone } from '../../../domain/feed';
import { overlaps, rangeOfLabel } from '../../../domain/highlight';
import type { AttackRange } from '../../../domain/highlight';
import { useHoverRange } from '../../../hooks/useHoverRange';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

const MS_PER_SECOND = 1_000;

// The feed shows this many items; the chart has all of them.
const MAX_FEED_ITEMS = 60;

// What the feed can be narrowed to.
type FeedFilter = 'all' | FeedTone;

const FILTERS: readonly FeedFilter[] = ['all', 'attack', 'heal'];

type EventFeedProps = {
  readonly events: readonly RoundEvent[];
  readonly isCalm: boolean;
  readonly highlight: AttackRange | null;
  readonly onHighlight: (range: AttackRange | null) => void;
};

// EventFeed tells each event in two halves: what happened, and what should
// happen because of it. Toasts announce new attacks, so the list itself is
// not a live region.
export default function EventFeed({ events, isCalm, highlight, onHighlight }: EventFeedProps): React.JSX.Element {
  const [filter, setFilter] = useState<FeedFilter>('all');
  const all = feedOf(events);
  const items = all.filter((item) => filter === 'all' || item.tone === filter).slice(0, MAX_FEED_ITEMS);

  return (
    <Card
      title={messages.feed.title}
      titleId="feed-title"
      aside={<FeedFilters items={all} filter={filter} onFilter={setFilter} />}
      className={styles.feed}
      bodyClassName={styles.feedBody}
    >
      {items.length === 0 ? (
        <p className={styles.feedEmpty}>{isCalm ? messages.feed.calm : messages.feed.empty}</p>
      ) : (
        <ol className={styles.feedList}>
          {items.map((item) => (
            <FeedEntry key={item.key} item={item} highlight={highlight} onHighlight={onHighlight} />
          ))}
        </ol>
      )}
    </Card>
  );
}

type FeedFiltersProps = {
  readonly items: readonly FeedItem[];
  readonly filter: FeedFilter;
  readonly onFilter: (filter: FeedFilter) => void;
};

function FeedFilters({ items, filter, onFilter }: FeedFiltersProps): React.JSX.Element {
  return (
    <div className={styles.pills} role="group" aria-label={messages.feed.filterLabel}>
      {FILTERS.map((option) => (
        <button
          key={option}
          type="button"
          className="btn btn-pill"
          aria-pressed={option === filter}
          onClick={() => onFilter(option)}
        >
          {format(messages.feed.filters[option], { count: countOf(items, option) })}
        </button>
      ))}
    </div>
  );
}

type FeedEntryProps = {
  readonly item: FeedItem;
  readonly highlight: AttackRange | null;
  readonly onHighlight: (range: AttackRange | null) => void;
};

function FeedEntry({ item, highlight, onHighlight }: FeedEntryProps): React.JSX.Element {
  const ref = useRef<HTMLLIElement>(null);
  const range = rangeOfLabel(item.label);
  const isHot = range !== null && overlaps(highlight, range.from, range.to);

  useHoverRange(ref, range, onHighlight);

  return (
    <li ref={ref} className={styles.feedItem} data-tone={item.tone} data-hot={isHot}>
      <span className={styles.feedMark} data-wide={(item.label?.length ?? 0) > 2} aria-hidden="true">
        {item.label ?? '·'}
      </span>
      <span className={styles.feedText}>
        <span className={styles.feedWhat}>
          {item.what}
          {item.count > 1 ? (
            <span className={styles.feedCount}>{format(messages.feed.times, { count: item.count })}</span>
          ) : null}
        </span>
        {item.code === '' ? null : <code className={styles.feedCode}>{item.code}</code>}
        {item.expected === '' ? null : (
          <span className={styles.feedWhy}>
            <Glossed text={item.expected} />
          </span>
        )}
      </span>
      <time className={styles.feedTime}>{timeOf(item)}</time>
    </li>
  );
}

// timeOf is when the item happened: one moment, or the span of a group.
function timeOf(item: FeedItem): string {
  const from = formatClock(item.fromMs / MS_PER_SECOND);

  return item.count > 1 ? `${from}–${formatClock(item.toMs / MS_PER_SECOND)}` : from;
}

function countOf(items: readonly FeedItem[], filter: FeedFilter): number {
  return items
    .filter((item) => filter === 'all' || item.tone === filter)
    .reduce((total, item) => total + item.count, 0);
}
