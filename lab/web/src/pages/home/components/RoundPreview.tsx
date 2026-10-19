import type { AttackId } from '../../../api/ids';
import { attackCopy } from '../../../domain/attacks';
import { formatDuration } from '../../../domain/duration';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './Composer.module.css';

type RoundPreviewProps = { readonly attack: AttackId; readonly durationSeconds: number };

// RoundPreview says, before anything starts, what the round will do and what
// it should end with.
export default function RoundPreview({ attack, durationSeconds }: RoundPreviewProps): React.JSX.Element {
  const { preview } = messages;
  const steps = [
    { title: format(preview.attack, { duration: formatDuration(durationSeconds) }), detail: preview.attackDetail },
    { title: preview.heal, detail: preview.healDetail },
    { title: preview.check, detail: preview.checkDetail },
    { title: preview.verdict, detail: preview.verdictDetail },
  ];

  return (
    <>
      <ol className={styles.previewList}>
        {steps.map((step) => (
          <li key={step.title}>
            <span>
              <strong>{step.title}</strong> {step.detail}
            </span>
          </li>
        ))}
      </ol>
      <p className={styles.expect}>
        <strong>{preview.expectTitle}</strong>
        {attackCopy(attack).expect}
      </p>
    </>
  );
}
