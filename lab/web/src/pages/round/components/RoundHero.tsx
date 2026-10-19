import type { RoundId } from '../../../api/ids';
import type { Replay } from '../../../api/replay';
import { StatusIntro } from '../../../components/ui/StatusIntro';
import type { StatusTone } from '../../../components/ui/StatusIntro';
import { expectationOf } from '../../../domain/headline';
import type { Phase } from '../../../domain/phase';
import { PAGE_HEADING_ID } from '../../../hooks/useRouteFocus';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import PhaseStepper from './PhaseStepper';
import RoundActions from './RoundActions';
import styles from './RoundPage.module.css';

type RoundHeroProps = {
  readonly round: RoundId;
  readonly replay: Replay;
  readonly phase: Phase;
  readonly headline: string;
  readonly elapsedSeconds: number;
  readonly isConnected: boolean;
};

// RoundHero opens the round: where it is, in one sentence, what it is doing
// to the bank and what should come of it.
export default function RoundHero({
  round,
  replay,
  phase,
  headline,
  elapsedSeconds,
  isConnected,
}: RoundHeroProps): React.JSX.Element {
  const isFinished = phase === 'verdict';
  const eyebrow = format(isFinished ? messages.round.eyebrowDone : messages.round.eyebrowLive, {
    round,
    scenario: replay.scenario,
  });

  return (
    <StatusIntro
      tone={toneOf(replay, isFinished)}
      eyebrow={eyebrow}
      title={headline}
      titleId={PAGE_HEADING_ID}
      headingLevel={1}
      isLive={!isFinished}
      description={
        <>
          {replay.description === '' ? null : <p>{replay.description}</p>}
          <p className={styles.expectation}>{expectationOf(replay.defenses)}</p>
          {replay.isStopped ? <p className={styles.notice}>{messages.round.stopped}</p> : null}
          {isConnected ? null : (
            <p className={styles.notice} role="status">
              {messages.round.reconnecting}
            </p>
          )}
        </>
      }
      footer={
        <div className={styles.heroFooter}>
          <PhaseStepper phase={phase} elapsedSeconds={elapsedSeconds} durationSeconds={replay.durationSeconds} />
          <RoundActions round={round} isFinished={isFinished} />
        </div>
      }
    />
  );
}

function toneOf(replay: Replay, isFinished: boolean): StatusTone {
  if (!isFinished) {
    return 'info';
  }

  switch (replay.verdict) {
    case 'pass':
      return 'healthy';
    case 'fail':
      return 'danger';
    case 'inconclusive':
    case null:
      return 'warning';
  }
}
