import { Shuffle } from 'lucide-react';
import { useRef } from 'react';

import type { AttackId } from '../../api/ids';
import { attackCopy } from '../../domain/attacks';
import type { Bet } from '../../domain/bets';
import { formatDuration } from '../../domain/duration';
import type { Reel, SurpriseDraw } from '../../domain/surprise';
import { useDrawStage, type DrawStage } from '../../hooks/useDrawStage';
import { useModalDialog } from '../../hooks/useModalDialog';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import AttackIcon from '../AttackIcon';
import { Button } from '../ui/Button';
import styles from './DrawDialog.module.css';

// One card plus the gap between cards, in pixels, as the CSS draws them.
const CARD_STEP_PX = 180;
const BET_CHOICES = ['none', 'holds', 'breaks'] as const;

type BetChoice = (typeof BET_CHOICES)[number];

export type DrawSession = {
	readonly draw: SurpriseDraw;
	readonly reel: Reel;
	readonly startedAtMs: number;
	readonly spinMs: number;
};

type DrawDialogProps = {
	readonly session: DrawSession;
	readonly bet: Bet | null;
	readonly isStarting: boolean;
	readonly onBet: (bet: Bet | null) => void;
	readonly onRedraw: () => void;
	readonly onCancel: () => void;
	readonly onReady: (draw: SurpriseDraw) => void;
};

// DrawDialog is the surprise draw: a roulette of attacks that stops on one,
// the attack in a sentence and a 3-2-1 before the round starts.
export default function DrawDialog(props: DrawDialogProps): React.JSX.Element {
	const { session, bet, isStarting, onBet, onRedraw, onCancel, onReady } = props;
	const dialog = useRef<HTMLDialogElement>(null);
	const stage = useDrawStage({ startedAtMs: session.startedAtMs, spinMs: session.spinMs, onReady: handleReady });
	const isSpinning = stage.kind === 'spinning';
	const copy = attackCopy(session.draw.attack);

	useModalDialog(dialog);

	function handleReady(): void {
		onReady(session.draw);
	}

	function handleCancel(event: React.SyntheticEvent<HTMLDialogElement>): void {
		event.preventDefault();
		onCancel();
	}

	function handleBetChange(event: React.ChangeEvent<HTMLInputElement>): void {
		const choice = event.target.value;

		onBet(choice === 'holds' || choice === 'breaks' ? choice : null);
	}

	return (
		<dialog ref={dialog} className={styles.draw} aria-labelledby="draw-title" onCancel={handleCancel}>
			<p className={styles.eyebrow}>{messages.surprise.eyebrow}</p>
			<h2 id="draw-title" className={styles.title} aria-live="polite">
				{isSpinning ? messages.surprise.drawing : format(messages.surprise.landed, { attack: copy.name })}
			</h2>
			<ReelStrip key={session.startedAtMs} reel={session.reel} spinMs={session.spinMs} isSpinning={isSpinning} />
			<div className={styles.reveal} data-shown={!isSpinning}>
				<p className={styles.detail}>{copy.detail}</p>
				<p className={styles.duration}>{format(messages.surprise.duration, { duration: formatDuration(session.draw.durationSeconds) })}</p>
				<p className={styles.count} role="status">
					{countText(stage, isStarting)}
				</p>
			</div>
			<fieldset className={styles.bets} disabled={isStarting}>
				<legend className={styles.betLegend}>{messages.surprise.betLegend}</legend>
				{BET_CHOICES.map((choice) => (
					<label key={choice} className={styles.bet}>
						<input type="radio" name="bet" value={choice} checked={choiceOf(bet) === choice} onChange={handleBetChange} />
						{messages.surprise.bets[choice]}
					</label>
				))}
			</fieldset>
			<div className={styles.actions}>
				<Button onClick={onRedraw} disabled={isStarting}>
					<Shuffle size={16} aria-hidden="true" />
					{messages.surprise.redraw}
				</Button>
				<Button onClick={onCancel}>
					{messages.surprise.cancel} <span className={styles.kbd}>Esc</span>
				</Button>
			</div>
		</dialog>
	);
}

type ReelStripProps = { readonly reel: Reel; readonly spinMs: number; readonly isSpinning: boolean };

// ReelStrip only decorates: the title and the reveal say what was drawn.
function ReelStrip({ reel, spinMs, isSpinning }: ReelStripProps): React.JSX.Element {
	// The keyframes read the reel's stop and length from these variables.
	const motion = { '--shift': `${-reel.winnerIndex * CARD_STEP_PX}px`, '--spin': `${spinMs}ms` } as React.CSSProperties;

	return (
		<div className={styles.window} aria-hidden="true">
			<div className={styles.reel} style={motion}>
				{reel.cards.map((attack, index) => (
					<ReelCard key={index} attack={attack} isWinner={!isSpinning && index === reel.winnerIndex} />
				))}
			</div>
		</div>
	);
}

function ReelCard({ attack, isWinner }: { attack: AttackId; isWinner: boolean }): React.JSX.Element {
	return (
		<div className={styles.card} data-win={isWinner}>
			<AttackIcon attack={attack} className={styles.cardIcon} />
			<b>{attackCopy(attack).name}</b>
		</div>
	);
}

function choiceOf(bet: Bet | null): BetChoice {
	return bet ?? 'none';
}

function countText(stage: DrawStage, isStarting: boolean): string {
	if (isStarting) {
		return messages.surprise.starting;
	}

	switch (stage.kind) {
		case 'spinning':
			return '';
		case 'counting':
			return format(messages.surprise.countdown, { seconds: stage.left });
		case 'ready':
			return messages.surprise.starting;
	}
}
