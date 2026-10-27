import { useRef } from 'react';

import type { RoundEvent } from '../../api/replay';
import { formatClock } from '../../domain/duration';
import { isAttackEvent, isDefenseEvent, isHealStart } from '../../domain/phase';
import { clusterMarkers } from '../../domain/timeline';
import { useElementSize } from '../../hooks/useElementSize';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';

const MS_PER_SECOND = 1_000;
const PERCENT = 100;
// Chips closer than this, in pixels, merge into one: a range like "10–12"
// needs about this much room.
const CHIP_GAP_PX = 44;
const SCALE_STEPS = 4;

type ArenaTickerProps = {
	readonly events: readonly RoundEvent[];
	readonly spanSeconds: number;
	readonly nowSeconds: number;
};

export default function ArenaTicker({ events, spanSeconds, nowSeconds }: ArenaTickerProps): React.JSX.Element {
	const box = useRef<HTMLDivElement>(null);
	const track = useElementSize(box);
	const percentOf = (tMs: number): number => Math.min(PERCENT, (tMs / MS_PER_SECOND / spanSeconds) * PERCENT);
	const attacks = events.filter(isAttackEvent);
	const chips = clusterMarkers(
		attacks.map((event) => percentOf(event.tMs)),
		(CHIP_GAP_PX / track.width) * PERCENT,
	);
	const defenses = events.filter(isDefenseEvent);
	const heal = events.find(isHealStart);
	const scale = Array.from({ length: SCALE_STEPS + 1 }, (_, index) => (spanSeconds * index) / SCALE_STEPS);

	return (
		<div className={styles.ticker} role="img" aria-label={messages.arena.tickerLabel}>
			<div ref={box} className={styles.track}>
				{heal ? <span className={styles.cure} style={{ left: `${percentOf(heal.tMs)}%` }} /> : null}
				{defenses.map((event, index) => (
					<span key={`${event.tMs}-${index}`} className={styles.defenseDot} style={{ left: `${percentOf(event.tMs)}%` }} />
				))}
				{chips.map((chip) => (
					<span key={chip.first} className={styles.chip} style={{ left: `${chip.x}%` }}>
						{chip.label}
					</span>
				))}
				<span className={styles.nowLine} style={{ left: `${Math.min(PERCENT, (nowSeconds / spanSeconds) * PERCENT)}%` }} />
			</div>
			<div className={styles.scale}>
				{scale.map((seconds) => (
					<span key={seconds}>{formatClock(seconds)}</span>
				))}
			</div>
			<div className={styles.legend}>
				<span data-kind="attack">{messages.arena.legend.attack}</span>
				<span data-kind="defense">{messages.arena.legend.defense}</span>
				<span data-kind="cure">{messages.arena.legend.cure}</span>
			</div>
		</div>
	);
}
