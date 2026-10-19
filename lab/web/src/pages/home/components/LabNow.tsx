import { FlaskConical } from 'lucide-react';

import type { RoundSummary } from '../../../api/rounds';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './LabNow.module.css';

type LabNowProps = { readonly rounds: readonly RoundSummary[] };

// LabNow sums up the rounds the page read, and says over how many.
export default function LabNow({ rounds }: LabNowProps): React.JSX.Element {
  const operations = rounds.reduce(addOperations, 0);
  const broken = rounds.filter(isBroken).length;

  return (
    <aside className={styles.labNow} aria-labelledby="lab-now-title">
      <div className={styles.head}>
        <h2 id="lab-now-title" className={styles.eyebrow}>
          {messages.labNow.title}
        </h2>
        <span className={styles.icon} aria-hidden="true">
          <FlaskConical size={16} />
        </span>
      </div>
      <p className={styles.figure}>{operations.toLocaleString('pt-BR')}</p>
      <p className={styles.figureLabel}>{messages.labNow.operations}</p>
      <dl className={styles.figures}>
        <div>
          <dt>{messages.labNow.rounds}</dt>
          <dd>{rounds.length.toLocaleString('pt-BR')}</dd>
        </div>
        <div>
          <dt>{messages.labNow.broken}</dt>
          <dd data-tone={broken > 0 ? 'broken' : 'ok'}>{broken.toLocaleString('pt-BR')}</dd>
        </div>
      </dl>
      <p className={styles.note}>
        {rounds.length === 0 ? messages.labNow.empty : format(messages.labNow.window, { count: rounds.length })}
      </p>
    </aside>
  );
}

function addOperations(total: number, round: RoundSummary): number {
  return total + round.operations;
}

function isBroken(round: RoundSummary): boolean {
  return round.verdict === 'fail';
}
