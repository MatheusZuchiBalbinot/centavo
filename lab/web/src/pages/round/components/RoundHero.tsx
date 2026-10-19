import type { RoundId } from '../../../api/ids';
import type { Replay } from '../../../api/replay';
import { Button } from '../../../components/ui/Button';
import { Glossed } from '../../../components/ui/Glossed';
import { StatusIntro } from '../../../components/ui/StatusIntro';
import { useViewMode } from '../../../hooks/viewModeContext';
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
  readonly onOpenArena: () => void;
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
  onOpenArena,
}: RoundHeroProps): React.JSX.Element {
  const isFinished = phase === 'verdict';
  const isTechnical = useViewMode().mode === 'technical';
  const eyebrowMessage = isFinished ? messages.round.eyebrowDone : messages.round.eyebrowLive;
  const eyebrow = format(eyebrowMessage, { round });

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
          {replay.description === '' ? null : (
            <p>
              <Glossed text={replay.description} />
            </p>
          )}
          <p className={styles.expectation}>
            <Glossed text={expectationOf(replay.defenses)} />
          </p>
          {isTechnical ? <TechnicalFacts replay={replay} /> : null}
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
          <div className={styles.actionsRow}>
            <Button variant="primary" onClick={onOpenArena}>
              {messages.arena.open}
            </Button>
            <RoundActions round={round} isFinished={isFinished} />
          </div>
        </div>
      }
    />
  );
}

// TechnicalFacts are the round's identity for whoever wants to rerun it.
function TechnicalFacts({ replay }: { replay: Replay }): React.JSX.Element {
  const facts = [
    format(messages.round.factScenario, { scenario: replay.scenario }),
    format(messages.round.factSeed, { seed: replay.seed }),
    format(messages.round.factDefenses, { defenses: replay.defenses || '?' }),
  ];

  return (
    <p className={styles.technicalFacts}>
      {facts.map((fact) => (
        <code key={fact} className="badge badge-muted">
          {fact}
        </code>
      ))}
    </p>
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
