import type { AuditorReading, GuaranteeCheck } from '../../../api/replay';
import { Badge } from '../../../components/ui/Badge';
import type { BadgeTone } from '../../../components/ui/Badge';
import { Card } from '../../../components/ui/Card';
import { useViewMode } from '../../../hooks/viewModeContext';
import { Glossed } from '../../../components/ui/Glossed';
import { GUARANTEE_IDS, guaranteeCopy, guaranteeView } from '../../../domain/guarantees';
import type { GuaranteeId, GuaranteeView } from '../../../domain/guarantees';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

type GuaranteesProps = {
  readonly checks: readonly GuaranteeCheck[];
  readonly reading: AuditorReading | null;
  readonly isFinished: boolean;
  readonly titleId?: string;
};

// Guarantees lists the eight promises the checker holds the bank to, each
// with what is known about it so far.
export default function Guarantees({
  checks,
  reading,
  isFinished,
  titleId = 'guarantees-title',
}: GuaranteesProps): React.JSX.Element {
  const views = GUARANTEE_IDS.map((id) => ({ id, view: guaranteeView(id, checks, reading) }));
  const held = views.filter(isHeld).length;
  const note = isFinished ? format(messages.guarantees.held, { count: held }) : messages.guarantees.liveNote;

  return (
    <Card title={messages.guarantees.title} titleId={titleId} aside={<span className="card-note">{note}</span>}>
      <ul className={styles.guaranteeList}>
        {views.map(({ id, view }) => (
          <GuaranteeItem key={id} id={id} view={view} isFinished={isFinished} />
        ))}
      </ul>
    </Card>
  );
}

type GuaranteeItemProps = { readonly id: GuaranteeId; readonly view: GuaranteeView; readonly isFinished: boolean };

function GuaranteeItem({ id, view, isFinished }: GuaranteeItemProps): React.JSX.Element {
  const copy = guaranteeCopy(id);
  const isTechnical = useViewMode().mode === 'technical';
  const { label, tone } = describeView(view);

  return (
    <li className={styles.guarantee} data-tone={tone} data-tooltip={`${copy.why} ${copy.threats}`}>
      {isTechnical ? <span className={styles.guaranteeId}>{id}</span> : null}
      <span className={styles.guaranteeText}>
        <span className={styles.guaranteeName}>{isFinished ? copy.name : copy.short}</span>
        {isFinished ? (
          <span className={styles.guaranteeDetail}>
            <Glossed text={copy.detail} />
          </span>
        ) : null}
      </span>
      <Badge tone={tone}>{label}</Badge>
    </li>
  );
}

function isHeld({ view }: { view: GuaranteeView }): boolean {
  return view.kind === 'final' && view.status === 'ok';
}

function describeView(view: GuaranteeView): { label: string; tone: BadgeTone } {
  const { state } = messages.guarantees;

  switch (view.kind) {
    case 'final':
      return describeFinal(view);
    case 'live':
      return view.isHolding ? { label: state.liveOk, tone: 'success' } : { label: state.liveBroken, tone: 'danger' };
    case 'later':
      return { label: state.later, tone: 'muted' };
  }
}

function describeFinal(view: Extract<GuaranteeView, { kind: 'final' }>): { label: string; tone: BadgeTone } {
  switch (view.status) {
    case 'ok':
      return { label: messages.guarantees.state.ok, tone: 'success' };
    case 'failed':
      return { label: format(messages.guarantees.violations, { count: view.violations }), tone: 'danger' };
    case 'not_checked':
      return { label: messages.guarantees.state.not_checked, tone: 'muted' };
  }
}
