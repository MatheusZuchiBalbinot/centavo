import { formatClock } from '../../../domain/duration';
import { PHASES, type Phase } from '../../../domain/phase';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

type PhaseStepperProps = {
	readonly phase: Phase;
	readonly elapsedSeconds: number;
	readonly durationSeconds: number;
};

type StepState = 'done' | 'current' | 'upcoming';

// PhaseStepper is the round's four steps as segments, the way the pyle shows
// an instance group: done ones filled, the current one moving.
export default function PhaseStepper({ phase, elapsedSeconds, durationSeconds }: PhaseStepperProps): React.JSX.Element {
	const current = PHASES.indexOf(phase);

	return (
		<ol className={styles.stepper} aria-label={messages.round.phasesLabel}>
			{PHASES.map((step, index) => (
				<PhaseStep key={step} phase={step} state={stateOf(index, current, phase)} elapsedSeconds={elapsedSeconds} durationSeconds={durationSeconds} />
			))}
		</ol>
	);
}

type PhaseStepProps = {
	readonly phase: Phase;
	readonly state: StepState;
	readonly elapsedSeconds: number;
	readonly durationSeconds: number;
};

function PhaseStep({ phase, state, elapsedSeconds, durationSeconds }: PhaseStepProps): React.JSX.Element {
	const isAttackNow = phase === 'attack' && state === 'current';
	const fill = isAttackNow ? percentOf(elapsedSeconds, durationSeconds) : null;

	return (
		<li className={styles.step} data-state={state} aria-current={state === 'current' ? 'step' : undefined}>
			<span className={styles.stepBar}>{fill === null ? null : <span className={styles.stepFill} style={{ width: `${fill}%` }} />}</span>
			<button type="button" className={`term ${styles.stepName ?? ''}`} data-tooltip={messages.round.phases[phase].help}>
				{messages.round.phases[phase].name}
			</button>
			<span className={styles.stepWhen}>{whenOf(phase, state, elapsedSeconds, durationSeconds)}</span>
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

function percentOf(elapsedSeconds: number, durationSeconds: number): number {
	if (durationSeconds <= 0) {
		return 100;
	}

	return Math.min(100, Math.round((elapsedSeconds / durationSeconds) * 100));
}

function whenOf(step: Phase, state: StepState, elapsedSeconds: number, durationSeconds: number): string {
	if (state === 'done') {
		return messages.round.phaseDone;
	}

	if (state === 'upcoming') {
		return messages.round.phaseNext;
	}

	if (step === 'attack') {
		return `${formatClock(Math.min(elapsedSeconds, durationSeconds))} / ${formatClock(durationSeconds)}`;
	}

	return messages.round.phaseNow;
}
