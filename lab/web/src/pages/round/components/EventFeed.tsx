import type { RoundEvent } from '../../../api/replay';
import { Card } from '../../../components/ui/Card';
import { formatClock } from '../../../domain/duration';
import { feedOf } from '../../../domain/feed';
import type { FeedItem } from '../../../domain/feed';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

const MS_PER_SECOND = 1_000;

// The feed shows this many items; the chart has all of them.
const MAX_FEED_ITEMS = 60;

type EventFeedProps = { readonly events: readonly RoundEvent[]; readonly isCalm: boolean };

// EventFeed tells each event in two halves: what happened, and what should
// happen because of it. Toasts announce new attacks, so the list itself is
// not a live region.
export default function EventFeed({ events, isCalm }: EventFeedProps): React.JSX.Element {
  const items = feedOf(events).slice(0, MAX_FEED_ITEMS);

  return (
    <Card
      title={messages.feed.title}
      titleId="feed-title"
      aside={<span className="card-note">{messages.feed.order}</span>}
      className={styles.feed}
      bodyClassName={styles.feedBody}
    >
      {items.length === 0 ? (
        <p className={styles.feedEmpty}>{isCalm ? messages.feed.calm : messages.feed.empty}</p>
      ) : (
        <ol className={styles.feedList}>
          {items.map((item) => (
            <FeedEntry key={item.key} item={item} />
          ))}
        </ol>
      )}
    </Card>
  );
}

function FeedEntry({ item }: { item: FeedItem }): React.JSX.Element {
  return (
    <li className={styles.feedItem} data-tone={item.tone}>
      <span className={styles.feedMark} aria-hidden="true">
        {item.number ?? '·'}
      </span>
      <span className={styles.feedText}>
        <span className={styles.feedWhat}>{item.what}</span>
        {item.code === '' ? null : <code className={styles.feedCode}>{item.code}</code>}
        {item.expected === '' ? null : <span className={styles.feedWhy}>{item.expected}</span>}
      </span>
      <time className={styles.feedTime}>{formatClock(item.tMs / MS_PER_SECOND)}</time>
    </li>
  );
}
