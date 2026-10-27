import {
	Activity,
	Clock,
	Database,
	DatabaseBackup,
	Handshake,
	PowerOff,
	RotateCw,
	Search,
	Server,
	ShieldCheck,
	Snowflake,
	Timer,
	Users,
	VenetianMask,
	type LucideIcon,
} from 'lucide-react';

import type { RoundEvent } from '../../api/replay';
import { describeEvent } from '../../domain/feed';
import { pointOf, type MapFrame } from '../../domain/mapGeometry';
import type { NodeView } from '../../domain/systemState';
import { isSameTarget, targetKeyOf, type MapNode, type MapTarget, type NodeId } from '../../domain/topology';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';
import { INSPECTOR_ID } from './ArenaInspector';

const ICONS: Readonly<Record<NodeId, LucideIcon>> = {
	clients: Users,
	api: Activity,
	worker1: Server,
	worker2: Server,
	postgres: Database,
	replica: DatabaseBackup,
	partner: Handshake,
	auditor: Search,
};

// A process out of the normal shows what happened to it instead of what it is.
const STATUS_ICONS: Readonly<Partial<Record<NodeView['status'], LucideIcon>>> = { down: PowerOff, starting: RotateCw, frozen: Snowflake };

const ICON_SIZE = 18;
const BADGE_ICON_SIZE = 12;
const TIMER_ICON_SIZE = 10;
const MS_PER_SECOND = 1_000;

const reactions: Readonly<Partial<Record<string, string>>> = messages.arena.reactions;

// shown is what fits on the node; text is what a screen reader hears.
type Badge = {
	readonly key: string;
	readonly text: string;
	readonly shown: string;
	readonly tone: 'info' | 'violet' | 'warning';
	readonly icon: LucideIcon | null;
};

// How long a process has been out, or how long its freeze still lasts.
type NodeTimer = { readonly seconds: number; readonly status: NodeView['status'] };

type MapNodeCardProps = {
	readonly node: MapNode;
	readonly view: NodeView;
	readonly frame: MapFrame;
	readonly nowMs: number;
	readonly operationsPerSecond: number;
	readonly selected: MapTarget | null;
	readonly onSelect: (target: MapTarget) => void;
};

export default function MapNodeCard({ node, view, frame, nowMs, operationsPerSecond, selected, onSelect }: MapNodeCardProps): React.JSX.Element {
	const target: MapTarget = { kind: 'node', id: node.id };
	const isSelected = isSameTarget(selected, target);
	const Icon = STATUS_ICONS[view.status] ?? (view.isLying ? VenetianMask : ICONS[node.id]);
	const name = messages.arena.nodes[node.id];
	const center = pointOf(node.id, frame);
	const status = statusText(node.id, view, operationsPerSecond);
	const timer = timerOf(view, nowMs);
	const badges = badgesOf(view);
	const reaction = view.reaction === null ? null : reactionText(view.reaction);
	const label = [`${name}: ${spokenStatus(status, timer)}`, ...badges.map((badge) => badge.text), reaction].filter(Boolean).join(', ');

	return (
		<button
			type="button"
			className={styles.node}
			data-status={view.status}
			data-hit={view.isHit}
			data-defending={view.isDefending}
			data-lying={view.isLying}
			data-selected={isSelected}
			data-target={targetKeyOf(target)}
			style={{ left: center.x, top: center.y }}
			data-tooltip={tooltipOf(node.id, view)}
			aria-label={label}
			aria-expanded={isSelected}
			aria-controls={isSelected ? INSPECTOR_ID : undefined}
			onClick={() => onSelect(target)}
		>
			{reaction === null ? null : (
				<span key={view.reaction?.tMs} className={styles.reaction} aria-hidden="true">
					<ShieldCheck size={BADGE_ICON_SIZE} />
					{reaction}
				</span>
			)}
			<span className={styles.nodeIcon} aria-hidden="true">
				<Icon size={ICON_SIZE} />
			</span>
			<span className={styles.nodeText}>
				<span className={styles.nodeName}>{name}</span>
				<span className={styles.nodeStatus}>{status}</span>
			</span>
			{timer === null ? null : (
				<span className={styles.nodeTimer} data-status={timer.status} aria-hidden="true">
					<Timer size={TIMER_ICON_SIZE} />
					{format(messages.arena.timer, { seconds: timer.seconds })}
				</span>
			)}
			<NodeBadges badges={badges} />
		</button>
	);
}

function NodeBadges({ badges }: { badges: readonly Badge[] }): React.JSX.Element | null {
	if (badges.length === 0) {
		return null;
	}

	return (
		<span className={styles.nodeBadges} aria-hidden="true">
			{badges.map((badge) => (
				<span key={badge.key} className={styles.nodeBadge} data-tone={badge.tone}>
					{badge.icon === null ? null : <badge.icon size={BADGE_ICON_SIZE} />}
					{badge.shown}
				</span>
			))}
		</span>
	);
}

function badgesOf(view: NodeView): readonly Badge[] {
	const badges: Badge[] = [];

	if (view.isLeader) {
		badges.push({ key: 'leader', text: messages.arena.leader, shown: messages.arena.leader, tone: 'info', icon: null });
	}

	if (view.isLying) {
		badges.push({ key: 'lying', text: messages.arena.lying, shown: messages.arena.lying, tone: 'violet', icon: VenetianMask });
	}

	if (view.clockSkew !== null) {
		badges.push({ key: 'skew', text: format(messages.arena.skew, { offset: view.clockSkew }), shown: view.clockSkew, tone: 'warning', icon: Clock });
	}

	return badges;
}

function statusText(id: NodeId, view: NodeView, operationsPerSecond: number): string {
	if (id === 'clients') {
		return format(messages.arena.clientsRate, { rate: Math.round(operationsPerSecond) });
	}

	return messages.arena.status[view.status];
}

function timerOf(view: NodeView, nowMs: number): NodeTimer | null {
	if (view.status === 'frozen') {
		return view.frozenUntilMs === null ? null : { seconds: secondsBetween(nowMs, view.frozenUntilMs), status: view.status };
	}

	const isOut = view.status === 'down' || view.status === 'starting';

	if (!isOut) {
		return null;
	}

	return { seconds: secondsBetween(view.statusSinceMs, nowMs), status: view.status };
}

// The timer is drawn as a badge; a screen reader hears it with the status.
function spokenStatus(status: string, timer: NodeTimer | null): string {
	if (timer === null) {
		return status;
	}

	if (timer.status === 'frozen') {
		return format(messages.arena.frozenFor, { seconds: timer.seconds });
	}

	return format(messages.arena.statusFor, { status, seconds: timer.seconds });
}

function secondsBetween(fromMs: number, toMs: number): number {
	return Math.max(0, Math.ceil((toMs - fromMs) / MS_PER_SECOND));
}

function reactionText(event: RoundEvent): string {
	return format(reactions[event.detail] ?? messages.arena.reactionUnknown, { count: event.count });
}

function tooltipOf(id: NodeId, view: NodeView): string {
	const help = messages.arena.nodeHelp[id];
	const last = view.lastEvent === null ? '' : ` ${describeEvent(view.lastEvent).what}`;

	return `${help}${last} ${messages.arena.nodeOpen}`;
}
