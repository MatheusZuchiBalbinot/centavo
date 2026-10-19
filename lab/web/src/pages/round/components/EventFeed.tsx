import type { RoundEvent } from '../../../api/replay';
import { formatClock } from '../../../domain/duration';
import { feedOf } from '../../../domain/feed';
import type { FeedItem } from '../../../domain/feed';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

const MS_PER_SECOND = 1_000;

// The feed shows this many items; the timeline has all of them.
const MAX_FEED_ITEMS = 60;

type EventFeedProps = { readonly events: readonly RoundEvent[]; readonly isCalm: boolean };

// EventFeed tells each event in two halves: what happened, and what should
// happen because of it. Toasts announce new attacks, so the list itself is
// not a live region.
export default function EventFeed({ events, isCalm }: EventFeedProps): React.JSX.Element {
  const items = feedOf(events).slice(0, MAX_FEED_ITEMS);

  return (
    <section className={styles.feed} aria-labelledby="feed-title">
      <div className={styles.feedHead}>
        <h2 id="feed-title" className={styles.sectionTitle}>
          {messages.feed.title}
        </h2>
        <span className={styles.muted}>{messages.feed.order}</span>
      </div>
      {items.length === 0 ? (
        <p className={styles.feedEmpty}>{isCalm ? messages.feed.calm : messages.feed.empty}</p>
      ) : (
        <ol className={styles.feedList}>
          {items.map((item) => (
            <FeedEntry key={item.key} item={item} />
          ))}
        </ol>
      )}
    </section>
  );
}

function FeedEntry({ item }: { item: FeedItem }): React.JSX.Element {
  return (
    <li className={styles.feedItem} data-tone={item.tone}>
      <time className={styles.feedTime}>{formatClock(item.tMs / MS_PER_SECOND)}</time>
      <span className={styles.feedKind}>
        {item.number === null ? null : <span className={styles.feedNumber}>{item.number}</span>}
        {messages.feed.kinds[item.tone]}
      </span>
      <span className={styles.feedWhat}>{item.what}</span>
      {item.expected === '' ? null : <span className={styles.feedWhy}>{item.expected}</span>}
    </li>
  );
}
