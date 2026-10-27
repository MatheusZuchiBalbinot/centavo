import { useRef } from 'react';

import type { RoundEvent } from '../../api/replay';
import { describeEvent } from '../../domain/feed';
import { manualActionsOf } from '../../domain/manualAttacks';
import { curveOf, frameOf, pointOf, type MapFrame } from '../../domain/mapGeometry';
import type { LinkView, SystemState } from '../../domain/systemState';
import { isSameTarget, linksOf, MAP_LINKS, MAP_NODES, nodesOf, targetKeyOf, type MapLink, type MapTarget, type NodeId } from '../../domain/topology';
import { useElementSize } from '../../hooks/useElementSize';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';
import { INSPECTOR_ID } from './ArenaInspector';
import MapNodeCard from './MapNodeCard';
import MapPackets, { BUSY_LINKS, totalOf, type Traffic } from './MapPackets';

const HALF = 0.5;

// The clients' links thicken with the load: one more pixel for every this
// many operations a second, up to a few.
const BASE_STROKE = 2;
const OPERATIONS_PER_PIXEL = 15;
const MAX_EXTRA_STROKE = 4;

// Only the links a person can attack get a button; the lab's own links are
// never faulted.
const ATTACKABLE_LINKS: readonly MapLink[] = MAP_LINKS.filter(isAttackable);

type SystemMapProps = {
	readonly state: SystemState;
	readonly traffic: Traffic;
	readonly bubble: { readonly event: RoundEvent; readonly label: string } | null;
	readonly nowMs: number;
	readonly isLive: boolean;
	readonly selected: MapTarget | null;
	readonly onSelect: (target: MapTarget) => void;
};

export default function SystemMap({ state, traffic, bubble, nowMs, isLive, selected, onSelect }: SystemMapProps): React.JSX.Element {
	const box = useRef<HTMLDivElement>(null);
	const size = useElementSize(box);
	const layout = frameOf(size.width, size.height);
	const operationsPerSecond = totalOf(traffic);

	return (
		<div ref={box} className={styles.map} role="group" aria-label={messages.arena.mapLabel}>
			<svg className={styles.links} width={size.width} height={size.height} aria-hidden="true">
				{MAP_LINKS.map((link) => (
					<LinkLine key={link.id} link={link} view={state.links[link.id]} frame={layout} operationsPerSecond={operationsPerSecond} />
				))}
			</svg>
			{isLive ? <MapPackets state={state} frame={layout} traffic={traffic} /> : null}
			{ATTACKABLE_LINKS.map((link) => (
				<LinkButton key={link.id} link={link} view={state.links[link.id]} frame={layout} selected={selected} onSelect={onSelect} />
			))}
			{MAP_NODES.map((node) => (
				<MapNodeCard
					key={node.id}
					node={node}
					view={state.nodes[node.id]}
					frame={layout}
					nowMs={nowMs}
					operationsPerSecond={operationsPerSecond}
					selected={selected}
					onSelect={onSelect}
				/>
			))}
			{bubble ? <AttackBubble event={bubble.event} label={bubble.label} frame={layout} /> : null}
			{isLive ? <TrafficLegend /> : null}
		</div>
	);
}

type LinkLineProps = { readonly link: MapLink; readonly view: LinkView; readonly frame: MapFrame; readonly operationsPerSecond: number };

function LinkLine({ link, view, frame, operationsPerSecond }: LinkLineProps): React.JSX.Element {
	const extra = BUSY_LINKS.has(link.id) ? Math.min(MAX_EXTRA_STROKE, operationsPerSecond / OPERATIONS_PER_PIXEL) : 0;

	return <path className={styles.link} data-status={view.status} d={curveOf(link, frame).path} style={{ strokeWidth: BASE_STROKE + extra }} />;
}

type LinkButtonProps = {
	readonly link: MapLink;
	readonly view: LinkView;
	readonly frame: MapFrame;
	readonly selected: MapTarget | null;
	readonly onSelect: (target: MapTarget) => void;
};

function LinkButton({ link, view, frame, selected, onSelect }: LinkButtonProps): React.JSX.Element {
	const target: MapTarget = { kind: 'link', id: link.id };
	const isSelected = isSameTarget(selected, target);
	const { middle } = curveOf(link, frame);
	const name = messages.arena.links[link.id];
	const status = messages.arena.linkStatus[view.status];
	const isFaulted = view.status !== 'ok';
	const label = format(isFaulted ? messages.arena.linkButtonFault : messages.arena.linkButton, { name, status, toxic: view.toxic ?? '' });

	return (
		<button
			type="button"
			className={styles.linkButton}
			data-status={view.status}
			data-selected={isSelected}
			data-target={targetKeyOf(target)}
			style={{ left: middle.x, top: middle.y }}
			data-tooltip={isFaulted ? label : format(messages.arena.linkHelp, { name })}
			aria-label={label}
			aria-expanded={isSelected}
			aria-controls={isSelected ? INSPECTOR_ID : undefined}
			onClick={() => onSelect(target)}
		>
			{isFaulted ? status : <span className={styles.linkDot} aria-hidden="true" />}
		</button>
	);
}

function TrafficLegend(): React.JSX.Element {
	const { label, ok, fail, info } = messages.arena.traffic;

	return (
		<p className={styles.trafficLegend} aria-hidden="true">
			<span>{label}:</span>
			<span data-outcome="ok">{ok}</span>
			<span data-outcome="fail">{fail}</span>
			<span data-outcome="info">{info}</span>
		</p>
	);
}

type AttackBubbleProps = { readonly event: RoundEvent; readonly label: string; readonly frame: MapFrame };

function AttackBubble({ event, label, frame }: AttackBubbleProps): React.JSX.Element {
	const { what, expected } = describeEvent(event);
	const anchor = pointOf(anchorOf(event), frame);
	const isOnRight = anchor.x < frame.width * HALF;

	return (
		<div className={styles.bubble} data-side={isOnRight ? 'right' : 'left'} data-manual={event.isManual} style={{ left: anchor.x, top: anchor.y }}>
			<strong>{format(messages.arena.bubble, { label })}</strong>
			<span>{what}</span>
			{expected === '' ? null : <small>{expected}</small>}
		</div>
	);
}

function anchorOf(event: RoundEvent): NodeId {
	const node = nodesOf(event.target)[0];

	if (node !== undefined) {
		return node;
	}

	const link = MAP_LINKS.find((candidate) => candidate.id === linksOf(event.target)[0]);

	return link?.from ?? 'api';
}

function isAttackable(link: MapLink): boolean {
	return manualActionsOf({ kind: 'link', id: link.id }).length > 0;
}
