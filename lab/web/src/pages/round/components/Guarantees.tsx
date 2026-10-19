import type { AuditorReading, GuaranteeCheck } from '../../../api/replay';
import { Tag } from '../../../components/ui/Tag';
import type { TagTone } from '../../../components/ui/Tag';
import { GUARANTEE_IDS, guaranteeCopy, guaranteeView } from '../../../domain/guarantees';
import type { GuaranteeId, GuaranteeView } from '../../../domain/guarantees';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

type GuaranteesProps = {
  readonly checks: readonly GuaranteeCheck[];
  readonly reading: AuditorReading | null;
  readonly isFinished: boolean;
};

// Guarantees lists the eight guarantees: as chips while the round runs, with
// their full description once the checker has the last word.
export default function Guarantees({ checks, reading, isFinished }: GuaranteesProps): React.JSX.Element {
  return (
    <section aria-labelledby="guarantees-title">
      <h2 id="guarantees-title" className={styles.sectionTitle}>
        {messages.guarantees.title}
      </h2>
      <ul className={isFinished ? styles.guaranteeList : styles.guaranteeStrip}>
        {GUARANTEE_IDS.map((id) => (
          <GuaranteeItem key={id} id={id} view={guaranteeView(id, checks, reading)} isFinished={isFinished} />
        ))}
      </ul>
    </section>
  );
}

type GuaranteeItemProps = { readonly id: GuaranteeId; readonly view: GuaranteeView; readonly isFinished: boolean };

function GuaranteeItem({ id, view, isFinished }: GuaranteeItemProps): React.JSX.Element {
  const copy = guaranteeCopy(id);
  const { label, tone } = describeView(view);

  if (!isFinished) {
    return (
      <li className={styles.chip} data-tone={tone}>
        <span className={styles.chipDot} aria-hidden="true" />
        <strong>{copy.short}</strong>
        <span>{label}</span>
      </li>
    );
  }

  return (
    <li className={styles.guarantee}>
      <span className={styles.guaranteeId}>{id}</span>
      <span>
        <strong className={styles.guaranteeName}>{copy.name}</strong>
        <span className={styles.guaranteeDetail}>{copy.detail}</span>
      </span>
      <Tag tone={tone}>{label}</Tag>
    </li>
  );
}

function describeView(view: GuaranteeView): { label: string; tone: TagTone } {
  const { state } = messages.guarantees;

  switch (view.kind) {
    case 'final':
      return describeFinal(view);
    case 'live':
      return view.isHolding ? { label: state.liveOk, tone: 'ok' } : { label: state.liveBroken, tone: 'broken' };
    case 'later':
      return { label: state.later, tone: 'neutral' };
  }
}

function describeFinal(view: Extract<GuaranteeView, { kind: 'final' }>): { label: string; tone: TagTone } {
  switch (view.status) {
    case 'ok':
      return { label: messages.guarantees.state.ok, tone: 'ok' };
    case 'failed':
      return { label: format(messages.guarantees.violations, { count: view.violations }), tone: 'broken' };
    case 'not_checked':
      return { label: messages.guarantees.state.not_checked, tone: 'neutral' };
  }
}
