import type { RoundSummary } from '../../../api/rounds';
import { Sheet } from '../../../components/ui/Sheet';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './LabNow.module.css';

type LabNowProps = { readonly rounds: readonly RoundSummary[] };

// LabNow sums up the rounds the page read, and says over how many.
export default function LabNow({ rounds }: LabNowProps): React.JSX.Element {
  const operations = rounds.reduce(addOperations, 0);
  const broken = rounds.filter(isBroken).length;

  return (
    <Sheet as="aside" className={styles.labNow} aria-labelledby="lab-now-title">
      <h2 id="lab-now-title" className={styles.title}>
        {messages.labNow.title}
      </h2>
      {rounds.length === 0 ? (
        <p className={styles.note}>{messages.labNow.empty}</p>
      ) : (
        <>
          <dl className={styles.figures}>
            <dt>{messages.labNow.rounds}</dt>
            <dd>{rounds.length.toLocaleString('pt-BR')}</dd>
            <dt>{messages.labNow.operations}</dt>
            <dd>{operations.toLocaleString('pt-BR')}</dd>
            <dt>{messages.labNow.broken}</dt>
            <dd data-tone={broken > 0 ? 'broken' : 'ok'}>{broken.toLocaleString('pt-BR')}</dd>
          </dl>
          <p className={styles.note}>{format(messages.labNow.window, { count: rounds.length })}</p>
        </>
      )}
    </Sheet>
  );
}

function addOperations(total: number, round: RoundSummary): number {
  return total + round.operations;
}

function isBroken(round: RoundSummary): boolean {
  return round.verdict === 'fail';
}
