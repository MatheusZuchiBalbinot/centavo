import { Volume2, VolumeX, X } from 'lucide-react';
import { useLayoutEffect, useRef, useState } from 'react';

import type { AuditorReading, Replay } from '../../api/replay';
import { attacksText } from '../../domain/attacks';
import { formatClock } from '../../domain/duration';
import { brokenCount, GUARANTEE_IDS } from '../../domain/guarantees';
import { incidentsOf } from '../../domain/incidents';
import type { ManualAttackControl } from '../../domain/manualAttacks';
import { formatCents } from '../../domain/money';
import { narrate } from '../../domain/narrator';
import { isAttackEvent, type Phase } from '../../domain/phase';
import { systemStateAt } from '../../domain/systemState';
import { attackSecondsOf, spanOf } from '../../domain/timeline';
import { targetKeyOf, type MapTarget } from '../../domain/topology';
import { trafficAt } from '../../domain/traffic';
import { useArenaSound } from '../../hooks/useArenaSound';
import { useModalDialog } from '../../hooks/useModalDialog';
import { useSpaceKey } from '../../hooks/useSpaceKey';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { Button } from '../ui/Button';
import { LiveIndicator } from '../ui/LiveIndicator';
import styles from './Arena.module.css';
import ArenaInspector from './ArenaInspector';
import ArenaSummary from './ArenaSummary';
import ArenaTicker from './ArenaTicker';
import SystemMap from './SystemMap';
import VerdictStamp from './VerdictStamp';

const MS_PER_SECOND = 1_000;
const BUBBLE_MS = 6_000;
const PERCENT = 100;

export type ArenaProps = {
	readonly eyebrow: string;
	readonly title: string;
	readonly replay: Replay;
	readonly reading: AuditorReading | null;
	readonly phase: Phase;
	// Milliseconds since the round started.
	readonly nowMs: number;
	readonly ageSeconds: number | null;
	// The clock time of this render, for how old the money reading is.
	readonly wallMs: number;
	readonly betLine: string | null;
	readonly actions?: React.ReactNode;
	readonly isLive: boolean;
	// Given only while this lab runs the round and its attack phase is on.
	readonly manualAttack?: ManualAttackControl;
	readonly player?: React.ReactNode;
	readonly onWatchAgain?: () => void;
	readonly onSpace?: () => void;
	readonly onClose: () => void;
};

export default function Arena(props: ArenaProps): React.JSX.Element {
	const {
		eyebrow,
		title,
		replay,
		reading,
		phase,
		nowMs,
		ageSeconds,
		wallMs,
		betLine,
		actions,
		isLive,
		manualAttack,
		player,
		onWatchAgain,
		onSpace,
		onClose,
	} = props;
	const dialog = useRef<HTMLDialogElement>(null);
	const [selected, setSelected] = useState<MapTarget | null>(null);

	const sound = useArenaSound({ attacks: replay.events.filter(isAttackEvent).length, verdict: replay.verdict });

	useSpaceKey(dialog, onSpace);
	const isFinished = phase === 'verdict';
	const nowSeconds = nowMs / MS_PER_SECOND;
	const seen = replay.events.filter((event) => event.tMs <= nowMs);
	const state = systemStateAt(replay.events, nowMs);
	const traffic = trafficAt(replay.seconds, nowSeconds, isLive && !isFinished);
	const incidents = incidentsOf(replay.events, nowMs);
	const span = spanOf({
		durationSeconds: attackSecondsOf(replay),
		seconds: replay.seconds,
		lastEventSeconds: nowSeconds,
		nowSeconds: null,
	});

	useModalDialog(dialog);

	// showModal focuses the first button, Copy link; the dialog itself reads
	// its title first and leaves Tab to start at the top.
	useLayoutEffect(() => {
		dialog.current?.focus();
	}, []);

	// React passes a dialog's cancel up through its parents, so Esc on the
	// stop confirmation, which lives inside the arena, would close the arena
	// too; the arena only answers its own.
	// Esc first closes the inspector, then the arena.
	function handleCancel(event: React.SyntheticEvent<HTMLDialogElement>): void {
		const isOwnCancel = event.target === event.currentTarget;

		if (!isOwnCancel) {
			return;
		}

		event.preventDefault();

		if (selected !== null) {
			handleInspectorClose();

			return;
		}

		onClose();
	}

	function handleSelect(target: MapTarget): void {
		setSelected(target);
	}

	// The focus goes back to the node or link the inspector was about.
	function handleInspectorClose(): void {
		if (selected === null) {
			return;
		}

		const trigger = dialog.current?.querySelector<HTMLElement>(`[data-target="${targetKeyOf(selected)}"]`);

		setSelected(null);
		trigger?.focus();
	}

	return (
		<dialog ref={dialog} className={styles.arena} data-replay={!isLive} tabIndex={-1} aria-labelledby="arena-title" onCancel={handleCancel}>
			<header className={styles.head}>
				<div className={styles.headText}>
					<p className={styles.eyebrow}>{eyebrow}</p>
					<h2 id="arena-title" className={styles.title}>
						{title}
					</h2>
				</div>
				<AttackClock phase={phase} nowSeconds={nowSeconds} durationSeconds={attackSecondsOf(replay)} />
				{isLive && !isFinished ? (
					<LiveIndicator
						state="live"
						label={messages.round.live}
						age={ageSeconds === null ? undefined : format(messages.round.age, { seconds: ageSeconds })}
					/>
				) : null}
				<Button onClick={sound.toggle} aria-pressed={sound.isOn} data-tooltip={messages.arena.soundHelp}>
					{sound.isOn ? <Volume2 size={16} aria-hidden="true" /> : <VolumeX size={16} aria-hidden="true" />}
					{messages.arena.sound}
				</Button>
				{actions}
				<Button onClick={onClose} aria-label={messages.arena.close}>
					<X size={16} aria-hidden="true" />
					<span className={styles.kbd}>Esc</span>
				</Button>
			</header>
			<div className={styles.body}>
				<SystemMap
					state={state}
					traffic={traffic}
					bubble={isFinished ? null : bubbleOf(seen, nowMs)}
					nowMs={nowMs}
					isLive={!isFinished}
					selected={selected}
					onSelect={handleSelect}
				/>
				<aside className={styles.side} aria-label={messages.arena.sideLabel}>
					{selected === null ? (
						<ArenaSummary
							replay={replay}
							reading={reading}
							seen={seen}
							incidents={incidents}
							headline={narrate(replay.events, nowMs, phase)}
							isFinished={isFinished}
							isLive={isLive}
							wallMs={wallMs}
						/>
					) : (
						<ArenaInspector
							key={targetKeyOf(selected)}
							target={selected}
							state={state}
							events={seen}
							manualAttack={manualAttack}
							onClose={handleInspectorClose}
						/>
					)}
				</aside>
			</div>
			<ArenaTicker events={replay.events} spanSeconds={span} nowSeconds={nowSeconds} isLive={isLive && !isFinished} />
			{player}
			{isFinished ? (
				<VerdictStamp verdict={replay.verdict} facts={factsOf(replay)} betLine={betLine}>
					{onWatchAgain === undefined ? null : <Button onClick={onWatchAgain}>{messages.arena.watchAgain}</Button>}
					<Button variant="primary" onClick={onClose}>
						{messages.arena.seeRound}
					</Button>
				</VerdictStamp>
			) : null}
		</dialog>
	);
}

type AttackClockProps = { readonly phase: Phase; readonly nowSeconds: number; readonly durationSeconds: number };

function AttackClock({ phase, nowSeconds, durationSeconds }: AttackClockProps): React.JSX.Element {
	const shown = Math.min(nowSeconds, durationSeconds);
	const percent = durationSeconds > 0 ? Math.min(PERCENT, (shown / durationSeconds) * PERCENT) : PERCENT;

	return (
		<div className={styles.clock}>
			<div className={styles.clockRow}>
				<span>{messages.round.phases[phase].name}</span>
				<span>
					{formatClock(shown)} / {formatClock(durationSeconds)}
				</span>
			</div>
			<div className={styles.clockBar}>
				<i style={{ width: `${percent}%` }} />
			</div>
		</div>
	);
}

function bubbleOf(seen: Replay['events'], nowMs: number): { event: Replay['events'][number]; label: string } | null {
	const attacks = seen.filter(isAttackEvent);
	const last = attacks.at(-1);

	const isOld = last === undefined || nowMs - last.tMs > BUBBLE_MS;

	if (isOld) {
		return null;
	}

	return { event: last, label: String(attacks.length) };
}

function factsOf(replay: Replay): string {
	const attacks = replay.events.filter(isAttackEvent).length;
	const held = GUARANTEE_IDS.length - brokenCount(replay.checks);
	const isMoneyBroken = replay.checks.some((check) => check.id === 'I1' && check.status === 'failed');

	return format(messages.arena.facts, {
		attacks: attacksText(attacks),
		money: isMoneyBroken ? messages.round.moneyBrokenValue : formatCents(0),
		held,
	});
}
