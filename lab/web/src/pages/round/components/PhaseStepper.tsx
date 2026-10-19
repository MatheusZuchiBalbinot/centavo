import { formatDuration } from '../../../domain/duration';
import { PHASES } from '../../../domain/phase';
import type { Phase } from '../../../domain/phase';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

const FULL = 1;

type PhaseStepperProps = {
  readonly phase: Phase;
  readonly elapsedSeconds: number;
  readonly durationSeconds: number;
};

// PhaseStepper is the round's four steps; the attack's bar fills with time.
export default function PhaseStepper({ phase, elapsedSeconds, durationSeconds }: PhaseStepperProps): React.JSX.Element {
  const current = PHASES.indexOf(phase);

  return (
    <ol className={styles.stepper} aria-label={messages.round.phasesLabel}>
      {PHASES.map((step, index) => (
        <PhaseStep
          key={step}
          phase={step}
          state={stateOf(index, current, phase)}
          progress={progressOf(step, phase, elapsedSeconds, durationSeconds)}
          when={whenOf(step, elapsedSeconds, durationSeconds)}
        />
      ))}
    </ol>
  );
}

type StepState = 'done' | 'current' | 'upcoming';

type PhaseStepProps = {
  readonly phase: Phase;
  readonly state: StepState;
  readonly progress: number;
  readonly when: string;
};

function PhaseStep({ phase, state, progress, when }: PhaseStepProps): React.JSX.Element {
  const copy = messages.round.phases[phase];

  return (
    <li className={styles.step} data-state={state} aria-current={state === 'current' ? 'step' : undefined}>
      <span className={styles.stepTrack}>
        <span className={styles.stepFill} style={{ width: `${Math.round(progress * 100)}%` }} />
      </span>
      <span className={styles.stepName}>{copy.name}</span>
      <span className={styles.stepWhen}>{stepLabel(state, when)}</span>
    </li>
  );
}

function stateOf(index: number, current: number, phase: Phase): StepState {
  // The verdict is the end: once it is in, every step is done.
  const isDone = phase === 'verdict' || index < current;

  if (isDone) {
    return 'done';
  }

  return index === current ? 'current' : 'upcoming';
}

function progressOf(step: Phase, phase: Phase, elapsedSeconds: number, durationSeconds: number): number {
  const stepIndex = PHASES.indexOf(step);
  const currentIndex = PHASES.indexOf(phase);

  const isPast = stepIndex < currentIndex || phase === 'verdict';

  if (isPast) {
    return FULL;
  }

  if (stepIndex > currentIndex) {
    return 0;
  }

  const isUntimed = step !== 'attack' || durationSeconds <= 0;

  if (isUntimed) {
    return FULL;
  }

  return Math.min(FULL, elapsedSeconds / durationSeconds);
}

function whenOf(step: Phase, elapsedSeconds: number, durationSeconds: number): string {
  if (step !== 'attack') {
    return messages.round.phases[step].when;
  }

  return format(messages.round.phases.attack.when, {
    elapsed: formatDuration(Math.min(elapsedSeconds, durationSeconds)),
    total: formatDuration(durationSeconds),
  });
}

function stepLabel(state: StepState, when: string): string {
  if (state === 'done') {
    return messages.round.phaseDone;
  }

  return state === 'current' ? `${messages.round.phaseNow} · ${when}` : when;
}
