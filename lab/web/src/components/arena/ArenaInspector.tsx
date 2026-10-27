import { ArrowLeft } from 'lucide-react';
import { useLayoutEffect, useRef } from 'react';

import type { ManualAction } from '../../api/lab';
import type { RoundEvent } from '../../api/replay';
import { formatClock } from '../../domain/duration';
import { canAttackNow, manualActionsOf, manualAttackOf, type ManualAttackControl } from '../../domain/manualAttacks';
import type { SystemState } from '../../domain/systemState';
import { historyOf } from '../../domain/targetHistory';
import type { MapTarget } from '../../domain/topology';
import { messages } from '../../i18n/pt-BR';
import { Button } from '../ui/Button';
import styles from './Arena.module.css';

export const INSPECTOR_ID = 'arena-inspector';

const MS_PER_SECOND = 1_000;
const BACK_ICON_SIZE = 16;
const DESTRUCTIVE: ReadonlySet<ManualAction> = new Set(['kill', 'cut']);

type ArenaInspectorProps = {
	readonly target: MapTarget;
	readonly state: SystemState;
	readonly events: readonly RoundEvent[];
	// Absent while no live round of this lab is under attack.
	readonly manualAttack: ManualAttackControl | undefined;
	readonly onClose: () => void;
};

// Mounted with a key per target, so the focus lands on each new title.
export default function ArenaInspector({ target, state, events, manualAttack, onClose }: ArenaInspectorProps): React.JSX.Element {
	const title = useRef<HTMLHeadingElement>(null);
	const history = historyOf(target, events);

	useLayoutEffect(() => {
		title.current?.focus();
	}, []);

	return (
		<section id={INSPECTOR_ID} className={styles.inspector} aria-labelledby="arena-inspector-title">
			<Button onClick={onClose} className={styles.inspectorBack}>
				<ArrowLeft size={BACK_ICON_SIZE} aria-hidden="true" />
				{messages.arena.inspector.back}
			</Button>
			<div className={styles.card}>
				<h3 ref={title} id="arena-inspector-title" className={styles.inspectorTitle} tabIndex={-1}>
					{nameOf(target)}
				</h3>
				<p className={styles.inspectorStatus} data-status={statusKeyOf(target, state)}>
					{statusOf(target, state)}
				</p>
			</div>
			<ManualAttackPanel target={target} state={state} manualAttack={manualAttack} />
			<section className={styles.card} aria-labelledby="arena-history">
				<h4 id="arena-history" className={styles.cardTitle}>
					{messages.arena.inspector.history}
				</h4>
				{history.length === 0 ? (
					<p className={styles.hint}>{messages.arena.inspector.empty}</p>
				) : (
					<ol className={styles.history}>
						{history.map((line) => (
							<li key={line.key} data-tone={line.tone}>
								<time>{formatClock(line.tMs / MS_PER_SECOND)}</time>
								<span>{line.text}</span>
							</li>
						))}
					</ol>
				)}
			</section>
		</section>
	);
}

type ManualAttackPanelProps = {
	readonly target: MapTarget;
	readonly state: SystemState;
	readonly manualAttack: ManualAttackControl | undefined;
};

function ManualAttackPanel(props: ManualAttackPanelProps): React.JSX.Element {
	return (
		<section className={styles.card} aria-labelledby="arena-manual">
			<h4 id="arena-manual" className={styles.cardTitle}>
				{messages.arena.manual.title}
			</h4>
			<ManualAttackBody {...props} />
		</section>
	);
}

function ManualAttackBody({ target, state, manualAttack }: ManualAttackPanelProps): React.JSX.Element {
	const actions = manualActionsOf(target);

	if (actions.length === 0) {
		return <p className={styles.hint}>{messages.arena.manual.none}</p>;
	}

	if (manualAttack === undefined) {
		return <p className={styles.hint}>{messages.arena.manual.closed}</p>;
	}

	return <ManualActions target={target} actions={actions} isAvailable={canAttackNow(target, state)} manualAttack={manualAttack} />;
}

type ManualActionsProps = {
	readonly target: MapTarget;
	readonly actions: readonly ManualAction[];
	readonly isAvailable: boolean;
	readonly manualAttack: ManualAttackControl;
};

function ManualActions({ target, actions, isAvailable, manualAttack }: ManualActionsProps): React.JSX.Element {
	const { state } = manualAttack;
	const isDisabled = !isAvailable || state.status !== 'ready';
	const feedback = state.status === 'sending' ? null : state.feedback;

	function handleAttack(action: ManualAction): void {
		const attack = manualAttackOf(target, action);

		if (attack === null) {
			return;
		}

		manualAttack.launch(attack);
	}

	return (
		<>
			<p className={styles.hint}>{messages.arena.manual.help}</p>
			<div className={styles.manualActions}>
				{actions.map((action) => (
					<Button key={action} variant={DESTRUCTIVE.has(action) ? 'danger' : 'secondary'} disabled={isDisabled} onClick={() => handleAttack(action)}>
						{messages.arena.manual.actions[action]}
					</Button>
				))}
			</div>
			{isAvailable ? null : <p className={styles.hint}>{messages.arena.manual.busy}</p>}
			<p className={styles.manualFeedback} data-tone={feedback?.tone} role="status">
				{feedback?.text}
			</p>
		</>
	);
}

function nameOf(target: MapTarget): string {
	if (target.kind === 'node') {
		return messages.arena.nodes[target.id];
	}

	return messages.arena.links[target.id];
}

function statusKeyOf(target: MapTarget, state: SystemState): string {
	if (target.kind === 'node') {
		return state.nodes[target.id].status;
	}

	return state.links[target.id].status;
}

function statusOf(target: MapTarget, state: SystemState): string {
	if (target.kind === 'node') {
		return messages.arena.status[state.nodes[target.id].status];
	}

	return messages.arena.linkStatus[state.links[target.id].status];
}
