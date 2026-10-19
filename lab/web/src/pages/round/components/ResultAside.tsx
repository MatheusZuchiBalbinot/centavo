import type { Replay } from '../../../api/replay';
import { reportPath } from '../../../api/routes';
import { LinkButton } from '../../../components/ui/Button';
import { Sheet } from '../../../components/ui/Sheet';
import { Tag } from '../../../components/ui/Tag';
import { brokenCount } from '../../../domain/guarantees';
import { disabledDefenses } from '../../../domain/headline';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import { ROUTES } from '../../../routes';
import styles from './RoundPage.module.css';

type ResultAsideProps = { readonly replay: Replay };

// ResultAside puts the verdict next to what the defense profile predicted: a
// control round that breaks is the expected outcome, not a failure.
export default function ResultAside({ replay }: ResultAsideProps): React.JSX.Element {
  const disabled = disabledDefenses(replay.defenses);
  const isControl = disabled.length > 0;
  const expected = isControl
    ? format(messages.round.expectedFail, { defenses: disabled.join(', ') })
    : messages.round.expectedPass;
  const broken = brokenCount(replay.checks);
  const isMatch = isControl ? replay.verdict === 'fail' : replay.verdict === 'pass';

  return (
    <div className={styles.side}>
      <Sheet as="section" className={styles.sideCard} aria-labelledby="expected-title">
        <h2 id="expected-title" className={styles.sectionTitle}>
          {messages.round.expectedTitle}
        </h2>
        <dl className={styles.expectedList}>
          <div>
            <dt>{messages.round.expectedLabel}</dt>
            <dd>{expected}</dd>
          </div>
          <div>
            <dt>{messages.round.actualLabel}</dt>
            <dd>
              <Tag tone={replay.verdict === 'pass' ? 'ok' : 'broken'}>{verdictLabel(replay)}</Tag>{' '}
              {format(messages.round.brokenCount, { count: broken })}
            </dd>
          </div>
        </dl>
        <p className={styles.match} data-match={isMatch}>
          {matchText(isMatch, isControl)}
        </p>
      </Sheet>
      <Sheet as="section" className={styles.sideCard} aria-labelledby="next-title">
        <h2 id="next-title" className={styles.sectionTitle}>
          {messages.round.nextTitle}
        </h2>
        <div className={styles.actions}>
          <LinkButton to={ROUTES.home} variant="primary">
            {messages.round.again}
          </LinkButton>
          <a className={styles.reportLink} href={reportPath(replay.round)}>
            {messages.round.report}
          </a>
        </div>
      </Sheet>
    </div>
  );
}

function verdictLabel(replay: Replay): string {
  return replay.verdict === null ? messages.verdict.none : messages.verdict[replay.verdict];
}

function matchText(isMatch: boolean, isControl: boolean): string {
  if (!isMatch) {
    return messages.round.mismatch;
  }

  return isControl ? messages.round.matchControl : messages.round.match;
}
