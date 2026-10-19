import type { RoundSummary } from '../../../api/rounds';
import { reportPath } from '../../../api/routes';
import { Sheet } from '../../../components/ui/Sheet';
import { Tag } from '../../../components/ui/Tag';
import type { TagTone } from '../../../components/ui/Tag';
import { attacksText, totalAttacks } from '../../../domain/attacks';
import { formatDuration } from '../../../domain/duration';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RecentRounds.module.css';

// How many round cards the home page shows.
const CARDS_SHOWN = 4;

type RecentRoundsProps = { readonly rounds: readonly RoundSummary[] };

export default function RecentRounds({ rounds }: RecentRoundsProps): React.JSX.Element {
  const shown = rounds.slice(0, CARDS_SHOWN);

  return (
    <section className={styles.recent} aria-labelledby="recent-title">
      <h2 id="recent-title" className={styles.title}>
        {messages.recent.title}
      </h2>
      {shown.length === 0 ? (
        <p className={styles.empty}>{messages.recent.empty}</p>
      ) : (
        <ul className={styles.grid}>
          {shown.map((round) => (
            <li key={round.round}>
              <RoundCard round={round} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

type RoundCardProps = { readonly round: RoundSummary };

function RoundCard({ round }: RoundCardProps): React.JSX.Element {
  const meta = format(messages.recent.meta, {
    round: round.round,
    scenario: round.scenario,
    duration: formatDuration(round.seconds),
    operations: round.operations.toLocaleString('pt-BR'),
  });

  return (
    <Sheet as="article" className={styles.card} data-verdict={round.verdict ?? 'none'}>
      <Tag tone={toneOf(round)}>{verdictLabel(round)}</Tag>
      <p className={styles.sentence}>{sentenceOf(round)}</p>
      <p className={styles.meta}>{meta}</p>
      <a className={styles.report} href={reportPath(round.round)}>
        {format(messages.recent.report, { round: round.round })}
      </a>
    </Sheet>
  );
}

function toneOf(round: RoundSummary): TagTone {
  switch (round.verdict) {
    case 'pass':
      return 'ok';
    case 'fail':
      return 'broken';
    case 'inconclusive':
      return 'running';
    case null:
      return 'neutral';
  }
}

function verdictLabel(round: RoundSummary): string {
  if (round.verdict === null) {
    return messages.verdict.none;
  }

  return messages.verdict[round.verdict];
}

// sentenceOf tells the round in one line, the way the card headline reads.
function sentenceOf(round: RoundSummary): string {
  const attacks = totalAttacks(round.counters);

  switch (round.verdict) {
    case 'pass':
      return attacks === 0 ? messages.recent.passCalm : format(messages.recent.pass, { attacks: attacksText(attacks) });
    case 'fail':
      return format(messages.recent.fail, { attacks: attacksText(attacks) });
    case 'inconclusive':
      return messages.recent.inconclusive;
    case null:
      return messages.recent.unfinished;
  }
}
