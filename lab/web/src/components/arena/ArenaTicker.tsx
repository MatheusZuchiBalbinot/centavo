import { useRef } from 'react';

import type { RoundEvent } from '../../api/replay';
import { formatClock } from '../../domain/duration';
import { isAttackEvent, isDefenseEvent, isHealStart } from '../../domain/phase';
import { clusterMarkers } from '../../domain/timeline';
import { useElementSize } from '../../hooks/useElementSize';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';

const MS_PER_SECOND = 1_000;
const PERCENT = 100;
// Chips closer than this, in pixels, merge into one: a range like "10–12"
// needs about this much room.
const CHIP_GAP_PX = 44;
// A chip is centered on its moment, but never closer to the strip's ends than
// half a wide chip, so an attack in the first second still shows whole; the
// chips merge after that, so a moved chip never lands on its neighbor.
const CHIP_EDGE_PX = 26;
const SCALE_STEPS = 4;

type ArenaTickerProps = {
	readonly events: readonly RoundEvent[];
	readonly spanSeconds: number;
	readonly nowSeconds: number;
	readonly isLive: boolean;
};

// Two lanes on one time axis: the attacks, numbered as the log numbers them,
// and the defenses the bank registered.
export default function ArenaTicker({ events, spanSeconds, nowSeconds, isLive }: ArenaTickerProps): React.JSX.Element {
	const box = useRef<HTMLDivElement>(null);
	const track = useElementSize(box);
	const percentOf = (tMs: number): number => Math.min(PERCENT, (tMs / MS_PER_SECOND / spanSeconds) * PERCENT);
	const attacks = events.filter(isAttackEvent);
	const edge = (CHIP_EDGE_PX / track.width) * PERCENT;
	const chips = clusterMarkers(
		attacks.map((event) => Math.min(PERCENT - edge, Math.max(edge, percentOf(event.tMs)))),
		(CHIP_GAP_PX / track.width) * PERCENT,
	);
	const defenses = events.filter(isDefenseEvent);
	const heal = events.find(isHealStart);
	const scale = Array.from({ length: SCALE_STEPS + 1 }, (_, index) => (spanSeconds * index) / SCALE_STEPS);
	const nowPercent = Math.min(PERCENT, (nowSeconds / spanSeconds) * PERCENT);
	const nowText = isLive ? format(messages.arena.ticker.now, { clock: formatClock(nowSeconds) }) : formatClock(nowSeconds);
	const { legend } = messages.arena;

	return (
		<div className={styles.ticker} role="img" aria-label={messages.arena.tickerLabel}>
			<div className={styles.tickerHead}>
				<span className={styles.tickerTitle}>{messages.arena.ticker.title}</span>
				<span className={styles.legend}>
					<span data-kind="attack">{legend.attack}</span>
					<span data-kind="manual">{legend.manual}</span>
					<span data-kind="defense">{legend.defense}</span>
					<span data-kind="cure">{legend.cure}</span>
				</span>
			</div>
			<div className={styles.tickerGrid}>
				<span className={styles.lanes}>
					<span>{messages.arena.ticker.attacks}</span>
					<span>{messages.arena.ticker.defenses}</span>
				</span>
				<div ref={box} className={styles.track}>
					<span className={styles.elapsed} style={{ width: `${nowPercent}%` }} />
					{heal ? (
						<span className={styles.cure} style={{ left: `${percentOf(heal.tMs)}%` }}>
							<span>{legend.cure}</span>
						</span>
					) : null}
					{defenses.map((event, index) => (
						<span key={`${event.tMs}-${index}`} className={styles.defenseDot} style={{ left: `${percentOf(event.tMs)}%` }} />
					))}
					{chips.map((chip) => (
						<span key={chip.first} className={styles.chip} data-manual={isAllManual(attacks, chip.first, chip.last)} style={{ left: `${chip.x}%` }}>
							{chip.label}
						</span>
					))}
					<span className={styles.nowLine} style={{ left: `${nowPercent}%` }}>
						<span className={styles.nowLabel}>{nowText}</span>
					</span>
				</div>
				<span />
				<div className={styles.scale}>
					{scale.map((seconds) => (
						<span key={seconds}>{formatClock(seconds)}</span>
					))}
				</div>
			</div>
		</div>
	);
}

// first and last count from 1, as the chips do.
function isAllManual(attacks: readonly RoundEvent[], first: number, last: number): boolean {
	return attacks.slice(first - 1, last).every((event) => event.isManual);
}
