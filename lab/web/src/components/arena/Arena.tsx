import { ShieldCheck, Volume2, VolumeX, X } from 'lucide-react';
import { useLayoutEffect, useRef } from 'react';

import type { AuditorReading, GuaranteeCheck, Replay } from '../../api/replay';
import { attacksText } from '../../domain/attacks';
import { formatClock } from '../../domain/duration';
import { brokenCount, GUARANTEE_IDS, guaranteeCopy, guaranteeView, type GuaranteeId, type GuaranteeView } from '../../domain/guarantees';
import { formatCents } from '../../domain/money';
import { narrate } from '../../domain/narrator';
import { isAttackEvent, isDefenseEvent, type Phase } from '../../domain/phase';
import { systemStateAt } from '../../domain/systemState';
import { attackSecondsOf, spanOf } from '../../domain/timeline';
import { useArenaSound } from '../../hooks/useArenaSound';
import { useModalDialog } from '../../hooks/useModalDialog';
import { useSpaceKey } from '../../hooks/useSpaceKey';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { Button } from '../ui/Button';
import { LiveIndicator } from '../ui/LiveIndicator';
import styles from './Arena.module.css';
import ArenaTicker from './ArenaTicker';
import SystemMap from './SystemMap';
import VerdictStamp from './VerdictStamp';

const MS_PER_SECOND = 1_000;
// The bubble tells the newest attack for this long.
const BUBBLE_MS = 6_000;
const PERCENT = 100;

export type ArenaProps = {
	readonly eyebrow: string;
	readonly title: string;
	readonly replay: Replay;
	readonly reading: AuditorReading | null;
	readonly phase: Phase;
	// nowMs is the moment of the round the arena shows, from its start.
	readonly nowMs: number;
	readonly ageSeconds: number | null;
	// wallMs is the clock time of this render, for how old the money reading is.
	readonly wallMs: number;
	// betLine settles the bet made in the surprise draw, once there is a verdict.
	readonly betLine: string | null;
	readonly actions?: React.ReactNode;
	// isLive marks a round happening now; a replay plays a recorded one.
	readonly isLive: boolean;
	readonly player?: React.ReactNode;
	readonly onWatchAgain?: () => void;
	readonly onSpace?: () => void;
	readonly onClose: () => void;
};

// Arena is the round on one screen: the system with every attack landing on
// it, the money, what happens and how the bank answers, and the verdict.
export default function Arena(props: ArenaProps): React.JSX.Element {
	const { eyebrow, title, replay, reading, phase, nowMs, ageSeconds, wallMs, betLine, actions, isLive, player, onWatchAgain, onSpace, onClose } =
		props;
	const dialog = useRef<HTMLDialogElement>(null);

	const sound = useArenaSound({ attacks: replay.events.filter(isAttackEvent).length, verdict: replay.verdict });

	// Space pauses a replay while the arena itself has focus.
	useSpaceKey(dialog, onSpace);
	const isFinished = phase === 'verdict';
	const nowSeconds = nowMs / MS_PER_SECOND;
	const seen = replay.events.filter((event) => event.tMs <= nowMs);
	const state = systemStateAt(replay.events, nowMs);
	const narration = narrate(replay.events, nowMs, phase);
	const latest = replay.seconds.findLast((second) => second.t <= nowSeconds) ?? replay.seconds.at(-1);
	const operationsPerSecond = latest ? latest.ok + latest.fail + latest.info : 0;
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
	function handleCancel(event: React.SyntheticEvent<HTMLDialogElement>): void {
		const isOwnCancel = event.target === event.currentTarget;

		if (!isOwnCancel) {
			return;
		}

		event.preventDefault();
		onClose();
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
					operationsPerSecond={operationsPerSecond}
					bubble={isFinished ? null : bubbleOf(seen, nowMs)}
					nowMs={nowMs}
					isLive={!isFinished}
				/>
				<aside className={styles.side}>
					<MoneyCard replay={replay} reading={reading} isFinished={isFinished} isLive={isLive} wallMs={wallMs} />
					<section className={styles.card} aria-labelledby="arena-now">
						<h3 id="arena-now" className={styles.cardTitle}>
							{messages.arena.nowTitle}
						</h3>
						<p className={styles.now} aria-live="polite">
							{narration.now}
						</p>
						{narration.defense ? (
							<div className={styles.defense} data-waiting={narration.defense.isWaiting}>
								<span className={styles.shield} aria-hidden="true">
									<ShieldCheck size={16} />
								</span>
								<div>
									<p className={styles.defenseTitle}>{narration.defense.isWaiting ? messages.arena.defenseWaiting : messages.arena.defenseTitle}</p>
									<p className={styles.defenseText}>{narration.defense.text}</p>
								</div>
							</div>
						) : null}
					</section>
					<Stats replay={replay} seen={seen} />
					<GuaranteeChips replay={replay} reading={reading} />
				</aside>
			</div>
			<ArenaTicker events={replay.events} spanSeconds={span} nowSeconds={nowSeconds} />
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

type MoneyCardProps = {
	readonly replay: Replay;
	readonly reading: AuditorReading | null;
	readonly isFinished: boolean;
	readonly isLive: boolean;
	readonly wallMs: number;
};

function MoneyCard(props: MoneyCardProps): React.JSX.Element {
	const view = moneyViewOf(props);

	return (
		<section className={styles.card} aria-labelledby="arena-money">
			<h3 id="arena-money" className={styles.cardTitle}>
				{messages.arena.money}
			</h3>
			<p className={styles.money} data-broken={view.isBroken}>
				{view.value}
			</p>
			<p className={styles.hint}>{view.hint}</p>
		</section>
	);
}

type MoneyView = { readonly value: string; readonly hint: string; readonly isBroken: boolean };

// moneyViewOf is the money card's word: the checker's at the end, the live
// auditor's meanwhile, and nothing a replay did not record.
function moneyViewOf({ replay, reading, isFinished, isLive, wallMs }: MoneyCardProps): MoneyView {
	if (isFinished) {
		const isBroken = replay.checks.some((check) => check.id === 'I1' && check.status === 'failed');
		const value = isBroken ? messages.round.moneyBrokenValue : formatCents(0);

		return { value, hint: messages.arena.moneyHintDone, isBroken };
	}

	if (!isLive) {
		return { value: messages.arena.moneyUnread, hint: messages.arena.moneyHintReplay, isBroken: false };
	}

	const isUnread = reading === null || reading.isUnavailable;

	if (isUnread) {
		return { value: messages.arena.moneyUnread, hint: messages.round.moneyHintLive, isBroken: false };
	}

	const seconds = Math.max(0, Math.round((wallMs - reading.at.getTime()) / MS_PER_SECOND));

	return {
		value: formatCents(reading.sumCents),
		hint: format(messages.arena.moneyHint, { seconds }),
		isBroken: reading.sumCents !== 0,
	};
}

function Stats({ replay, seen }: { replay: Replay; seen: Replay['events'] }): React.JSX.Element {
	const operations = replay.seconds.reduce((total, second) => total + second.ok + second.fail + second.info, 0);
	const unknown = replay.seconds.reduce((total, second) => total + second.info, 0);
	const stats = [
		{ key: 'operations', value: operations, label: messages.arena.stats.operations },
		{ key: 'attacks', value: seen.filter(isAttackEvent).length, label: messages.arena.stats.attacks },
		{ key: 'defenses', value: seen.filter(isDefenseEvent).length, label: messages.arena.stats.defenses },
		{ key: 'unknown', value: unknown, label: messages.arena.stats.unknown },
	];

	return (
		<section className={`${styles.card ?? ''} ${styles.stats ?? ''}`} aria-label={messages.round.board}>
			{stats.map((stat) => (
				<p key={stat.key} className={styles.stat} data-kind={stat.key}>
					<b>{stat.value.toLocaleString('pt-BR')}</b>
					<span>{stat.label}</span>
				</p>
			))}
		</section>
	);
}

function GuaranteeChips({ replay, reading }: { replay: Replay; reading: AuditorReading | null }): React.JSX.Element {
	return (
		<section className={styles.card} aria-labelledby="arena-guarantees">
			<h3 id="arena-guarantees" className={styles.cardTitle}>
				{messages.arena.guarantees}
			</h3>
			<ul className={styles.chips}>
				{GUARANTEE_IDS.map((id) => (
					<GuaranteeChip key={id} id={id} view={guaranteeView(id, replay.checks, reading)} />
				))}
			</ul>
			<p className={styles.hint}>{messages.arena.guaranteesHint}</p>
		</section>
	);
}

function GuaranteeChip({ id, view }: { id: GuaranteeId; view: GuaranteeView }): React.JSX.Element {
	const copy = guaranteeCopy(id);

	return (
		<li className={styles.guaranteeChip} data-tone={toneOf(view)} data-tooltip={copy.name}>
			{copy.short}
		</li>
	);
}

function toneOf(view: GuaranteeView): 'ok' | 'broken' | 'later' {
	switch (view.kind) {
		case 'later':
			return 'later';
		case 'live':
			return view.isHolding ? 'ok' : 'broken';
		case 'final':
			return finalTone(view.status);
	}
}

function finalTone(status: GuaranteeCheck['status']): 'ok' | 'broken' | 'later' {
	if (status === 'not_checked') {
		return 'later';
	}

	return status === 'failed' ? 'broken' : 'ok';
}

// bubbleOf is the newest attack while it is still news, numbered as in the
// feed.
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
