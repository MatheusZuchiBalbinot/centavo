import { Fragment, memo, useMemo, useRef, useState } from 'react';

import type { RoundEvent, RoundTransfer } from '../../api/replay';
import { formatClock } from '../../domain/duration';
import {
	detailsOf,
	firstCursor,
	isManualOccurrence,
	movedCursor,
	occurrencesOf,
	rowsOf,
	type Occurrence,
	type OccurrenceCursor,
	type OccurrenceRow,
	type OccurrenceRows,
} from '../../domain/occurrences';
import { isHealStart } from '../../domain/phase';
import { useElementSize } from '../../hooks/useElementSize';
import { useEscapeKey } from '../../hooks/useEscapeKey';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';
import OccurrenceLegend from './OccurrenceLegend';

const MS_PER_SECOND = 1_000;
const SCALE_STEPS = 4;
const STRIP_HEIGHT = 50;
// The svg fills the strip at 100%: a width in pixels taken from the strip's
// own size would hold the arena's column at that width and never let it
// shrink. The measured width only places the dots.
// The rows' centers, top to bottom, and the size of their dots: an attack is
// rare and big, a transfer is one of thousands and small.
const ROW_Y: Readonly<Record<OccurrenceRow, number>> = { attack: 11, defense: 24, money: 35 };
const RADIUS: Readonly<Record<OccurrenceRow, number>> = { attack: 5, defense: 3.5, money: 2.5 };
// Transfers and PIX share the money row, one line each, so ten a second
// still read as two colors instead of one band.
const PIX_DROP = 8;
const ACTIVE_RING = 4;
const HOVER_PX = 8;
const CARD_WIDTH_PX = 300;

type OccurrenceStripProps = {
	readonly events: readonly RoundEvent[];
	readonly transfers: readonly RoundTransfer[];
	readonly spanSeconds: number;
	readonly nowSeconds: number;
	readonly isLive: boolean;
};

export default function OccurrenceStrip({ events, transfers, spanSeconds, nowSeconds, isLive }: OccurrenceStripProps): React.JSX.Element {
	const box = useRef<HTMLDivElement>(null);
	const { width } = useElementSize(box);
	// A long round has thousands of transfers: sorted when they change, not
	// on every tick of the clock.
	const occurrences = useMemo(() => occurrencesOf(events, transfers), [events, transfers]);
	const rows = useMemo(() => rowsOf(occurrences), [occurrences]);
	const [cursor, setCursor] = useState<OccurrenceCursor | null>(null);
	const spanMs = spanSeconds * MS_PER_SECOND;
	const nowMs = nowSeconds * MS_PER_SECOND;
	const xOf = (tMs: number): number => (Math.min(tMs, spanMs) / spanMs) * width;
	const active = cursor === null ? null : (rows[cursor.row][cursor.index] ?? null);
	const heal = events.find(isHealStart);
	const nowText = isLive ? format(messages.arena.occurrences.now, { clock: formatClock(nowSeconds) }) : formatClock(nowSeconds);
	const scale = Array.from({ length: SCALE_STEPS + 1 }, (_, index) => (spanSeconds * index) / SCALE_STEPS);

	// Esc closes the card without closing the arena around it.
	function handleEscape(event: KeyboardEvent): void {
		event.preventDefault();
		setCursor(null);
	}

	useEscapeKey(active !== null, handleEscape);

	// The dot under the pointer: the closest one in the row it points at,
	// counting both axes, since the money row holds two lines.
	function handlePointerMove(event: React.PointerEvent<SVGSVGElement>): void {
		const bounds = event.currentTarget.getBoundingClientRect();
		const x = event.clientX - bounds.left;
		const y = event.clientY - bounds.top;
		const row = rowAt(y);
		const index = closestTo(rows[row], { x, y, xOf });

		setCursor(index === -1 ? null : { row, index });
	}

	function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>): void {
		const from = cursor ?? firstCursor(rows, nowMs);
		const next = from === null ? null : movedCursor(rows, from, event.key);

		if (next === null) {
			return;
		}

		event.preventDefault();
		setCursor(next);
	}

	function handleFocus(): void {
		setCursor(firstCursor(rows, nowMs));
	}

	return (
		<div className={styles.ticker}>
			<div className={styles.tickerHead}>
				<span className={styles.tickerTitle}>{messages.arena.occurrences.title}</span>
				<span id="occurrences-hint" className={styles.tickerHint}>
					{messages.arena.occurrences.hint}
				</span>
				<OccurrenceLegend occurrences={occurrences} hasCure={heal !== undefined} />
			</div>
			<div className={styles.stripWrap} onPointerLeave={() => setCursor(null)}>
				<div
					ref={box}
					className={styles.strip}
					role="slider"
					tabIndex={0}
					aria-label={messages.arena.occurrences.label}
					aria-valuemin={0}
					aria-valuemax={Math.max(0, (cursor === null ? 0 : rows[cursor.row].length) - 1)}
					aria-valuenow={cursor?.index ?? 0}
					aria-valuetext={valueTextOf(active, cursor, rows)}
					aria-describedby="occurrences-hint"
					onKeyDown={handleKeyDown}
					onFocus={handleFocus}
					onBlur={() => setCursor(null)}
				>
					<svg width="100%" height={STRIP_HEIGHT} aria-hidden="true" onPointerMove={handlePointerMove}>
						<rect className={styles.elapsed} x={0} y={0} width={xOf(nowMs)} height={STRIP_HEIGHT} />
						{heal ? <CureBand x={xOf(heal.tMs)} width={width} /> : null}
						<OccurrenceDots occurrences={occurrences} width={width} spanMs={spanMs} />
						{active ? <ActiveRing occurrence={active} spanMs={spanMs} width={width} /> : null}
						<line className={styles.nowLine} x1={xOf(nowMs)} x2={xOf(nowMs)} y1={0} y2={STRIP_HEIGHT} />
					</svg>
					<span className={styles.nowLabel} style={{ left: xOf(nowMs) }}>
						{nowText}
					</span>
				</div>
				{active ? <OccurrenceCard occurrence={active} x={xOf(active.tMs)} width={width} /> : null}
			</div>
			<div className={styles.scale}>
				{scale.map((seconds) => (
					<span key={seconds}>{formatClock(seconds)}</span>
				))}
			</div>
		</div>
	);
}

type OccurrenceDotsProps = { readonly occurrences: readonly Occurrence[]; readonly width: number; readonly spanMs: number };

// Memoized: the clock ticks every second, the thousands of dots only change
// with the round.
const OccurrenceDots = memo(function OccurrenceDots({ occurrences, width, spanMs }: OccurrenceDotsProps): React.JSX.Element {
	return (
		<g>
			{occurrences.map((occurrence) => (
				<circle
					key={occurrence.key}
					className={styles.dot}
					data-kind={occurrence.kind}
					data-outcome={occurrence.row === 'money' ? occurrence.transfer.outcome : undefined}
					data-manual={isManualOccurrence(occurrence)}
					cx={dotX({ tMs: occurrence.tMs, spanMs, width, radius: RADIUS[occurrence.row] })}
					cy={yOf(occurrence)}
					r={RADIUS[occurrence.row]}
				/>
			))}
		</g>
	);
});

function ActiveRing({ occurrence, spanMs, width }: { occurrence: Occurrence; spanMs: number; width: number }): React.JSX.Element {
	const radius = RADIUS[occurrence.row];

	return (
		<circle className={styles.activeRing} cx={dotX({ tMs: occurrence.tMs, spanMs, width, radius })} cy={yOf(occurrence)} r={radius + ACTIVE_RING} />
	);
}

function CureBand({ x, width }: { x: number; width: number }): React.JSX.Element {
	return (
		<g className={styles.cure}>
			<rect x={x} y={0} width={Math.max(0, width - x)} height={STRIP_HEIGHT} />
		</g>
	);
}

type OccurrenceCardProps = { readonly occurrence: Occurrence; readonly x: number; readonly width: number };

// The slider's value text says the same to a screen reader, so the card
// itself is only for the eye.
function OccurrenceCard({ occurrence, x, width }: OccurrenceCardProps): React.JSX.Element {
	const details = detailsOf(occurrence);
	const left = Math.min(Math.max(0, x - CARD_WIDTH_PX / 2), Math.max(0, width - CARD_WIDTH_PX));

	return (
		<div className={styles.occurrenceCard} style={{ left }} aria-hidden="true">
			<strong className={styles.occurrenceTitle} data-kind={occurrence.kind}>
				{details.title}
			</strong>
			<p>{details.text}</p>
			<dl>
				{details.fields.map((field) => (
					<Fragment key={field.label}>
						<dt>{field.label}</dt>
						<dd>{field.value}</dd>
					</Fragment>
				))}
			</dl>
		</div>
	);
}

type DotPlace = { readonly tMs: number; readonly spanMs: number; readonly width: number; readonly radius: number };

// A dot at the round's first or last moment stays whole inside the strip.
function dotX({ tMs, spanMs, width, radius }: DotPlace): number {
	const x = (Math.min(tMs, spanMs) / spanMs) * width;

	return Math.min(Math.max(x, radius), Math.max(radius, width - radius));
}

function yOf(occurrence: Occurrence): number {
	return occurrence.kind === 'pix' ? ROW_Y.money + PIX_DROP : ROW_Y[occurrence.row];
}

type PointerAt = { readonly x: number; readonly y: number; readonly xOf: (tMs: number) => number };

// -1 when no dot of the row is within reach of the pointer.
function closestTo(row: readonly Occurrence[], { x, y, xOf }: PointerAt): number {
	let best = -1;
	let bestDistance = HOVER_PX * HOVER_PX;

	for (const [index, occurrence] of row.entries()) {
		const dx = xOf(occurrence.tMs) - x;

		if (Math.abs(dx) > HOVER_PX) {
			continue;
		}

		const dy = yOf(occurrence) - y;
		const distance = dx * dx + dy * dy;

		if (distance <= bestDistance) {
			best = index;
			bestDistance = distance;
		}
	}

	return best;
}

function rowAt(y: number): OccurrenceRow {
	if (y < (ROW_Y.attack + ROW_Y.defense) / 2) {
		return 'attack';
	}

	return y < (ROW_Y.defense + ROW_Y.money) / 2 ? 'defense' : 'money';
}

function valueTextOf(active: Occurrence | null, cursor: OccurrenceCursor | null, rows: OccurrenceRows): string {
	const copy = messages.arena.occurrences;

	if (active === null || cursor === null) {
		return copy.hint;
	}

	const details = detailsOf(active);
	const at = details.fields[0]?.value ?? '';
	const values = {
		row: copy.rows[cursor.row],
		index: cursor.index + 1,
		total: rows[cursor.row].length,
		title: details.title,
		text: details.text,
		at,
	};

	return format(copy.valueText, values);
}
