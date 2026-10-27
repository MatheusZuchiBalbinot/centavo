import type { Verdict } from '../../api/rounds';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';

// Pieces of confetti; their places come from their index, so every render
// draws the same burst.
const CONFETTI = 36;
const CONFETTI_COLORS = ['var(--success)', 'var(--primary)', 'var(--warning)', 'var(--violet)'] as const;
const GOLDEN_STEP = 0.618;
const PERCENT = 100;

type VerdictStampProps = {
	readonly verdict: Verdict | null;
	readonly facts: string;
	readonly betLine: string | null;
	readonly children: React.ReactNode;
};

export default function VerdictStamp({ verdict, facts, betLine, children }: VerdictStampProps): React.JSX.Element {
	const isPass = verdict === 'pass';

	return (
		<div className={styles.stampLayer}>
			{isPass ? <Confetti /> : null}
			<div className={styles.stamp} data-verdict={verdict ?? 'none'} role="status">
				<p className={styles.stampWord}>{stampWord(verdict)}</p>
				<p className={styles.stampFacts}>{facts}</p>
				{betLine === null ? null : <p className={styles.stampBet}>{betLine}</p>}
				<div className={styles.stampActions}>{children}</div>
			</div>
		</div>
	);
}

function Confetti(): React.JSX.Element {
	return (
		<div className={styles.confetti} aria-hidden="true">
			{Array.from({ length: CONFETTI }, (_, index) => (
				<span
					key={index}
					style={{
						left: `${((index * GOLDEN_STEP) % 1) * PERCENT}%`,
						background: CONFETTI_COLORS[index % CONFETTI_COLORS.length],
						animationDelay: `${(index % 12) * 0.07}s`,
					}}
				/>
			))}
		</div>
	);
}

function stampWord(verdict: Verdict | null): string {
	switch (verdict) {
		case 'pass':
			return messages.arena.stampPass;
		case 'fail':
			return messages.arena.stampFail;
		case 'inconclusive':
		case null:
			return messages.arena.stampInconclusive;
	}
}
