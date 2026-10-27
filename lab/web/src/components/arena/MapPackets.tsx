import type { CSSProperties } from 'react';

import { curveOf, type MapFrame } from '../../domain/mapGeometry';
import type { SystemState } from '../../domain/systemState';
import { MAP_LINKS, type LinkId, type MapLink } from '../../domain/topology';
import { totalOf, type Traffic } from '../../domain/traffic';
import styles from './Arena.module.css';

type Outcome = keyof Traffic;

// flow crosses the link; bounce turns back at --turn, against a cut or a
// process that is down.
type Motion = 'flow' | 'slow' | 'bounce';

type PacketStyle = CSSProperties & { readonly '--turn'?: string };

type Packet = { readonly key: string; readonly outcome: Outcome; readonly motion: Motion; readonly style: PacketStyle };

type PacketInput = { readonly link: MapLink; readonly state: SystemState; readonly frame: MapFrame; readonly traffic: Traffic };

// Only the clients' links carry the measured traffic; the others show that
// they are alive.
export const BUSY_LINKS: ReadonlySet<LinkId> = new Set(['client_api', 'api_pg']);

const MAX_PACKETS = 6;
const OPERATIONS_PER_PACKET = 6;
const PACKET_SPACING_S = 0.3;
const BOUNCING_PACKETS = 2;
const CUT_TURN = '48%';
const DOWN_TURN = '82%';

type MapPacketsProps = { readonly state: SystemState; readonly frame: MapFrame; readonly traffic: Traffic };

export default function MapPackets({ state, frame, traffic }: MapPacketsProps): React.JSX.Element {
	const packets = MAP_LINKS.flatMap((link) => packetsOf({ link, state, frame, traffic }));

	return (
		<div className={styles.packets} aria-hidden="true">
			{packets.map((packet) => (
				<span key={packet.key} className={styles.packet} data-outcome={packet.outcome} data-motion={packet.motion} style={packet.style} />
			))}
		</div>
	);
}

function packetsOf({ link, state, frame, traffic }: PacketInput): readonly Packet[] {
	const view = state.links[link.id];
	const isSourceUp = state.nodes[link.from].status === 'up';

	if (!isSourceUp) {
		return [];
	}

	const offsetPath = `path('${curveOf(link, frame).path}')`;
	const isTargetDown = state.nodes[link.to].status === 'down';

	const isBlocked = view.status === 'cut' || isTargetDown;

	if (isBlocked) {
		const turn = isTargetDown ? DOWN_TURN : CUT_TURN;

		return Array.from({ length: BOUNCING_PACKETS }, (_, index) => packetAt({ link, index, outcome: 'fail', motion: 'bounce', offsetPath, turn }));
	}

	const outcomes: readonly Outcome[] = BUSY_LINKS.has(link.id) ? outcomesOf(traffic) : ['ok'];
	const motion: Motion = view.status === 'slow' ? 'slow' : 'flow';

	return outcomes.map((outcome, index) => packetAt({ link, index, outcome, motion, offsetPath, turn: null }));
}

type PacketAtInput = {
	readonly link: MapLink;
	readonly index: number;
	readonly outcome: Outcome;
	readonly motion: Motion;
	readonly offsetPath: string;
	readonly turn: string | null;
};

function packetAt({ link, index, outcome, motion, offsetPath, turn }: PacketAtInput): Packet {
	const style: PacketStyle = { offsetPath, animationDelay: `${(index * PACKET_SPACING_S).toFixed(2)}s` };

	return { key: `${link.id}-${index}`, outcome, motion, style: turn === null ? style : { ...style, '--turn': turn } };
}

function outcomesOf(traffic: Traffic): readonly Outcome[] {
	const total = totalOf(traffic);

	if (total === 0) {
		return [];
	}

	const count = Math.min(MAX_PACKETS, Math.ceil(total / OPERATIONS_PER_PACKET));
	const failed = shareOf(traffic.fail, total, count);
	const unknown = Math.min(count - failed, shareOf(traffic.info, total, count));
	const done = Math.max(0, count - failed - unknown);

	return [...repeated('ok', done), ...repeated('fail', failed), ...repeated('info', unknown)];
}

// Any share at all gets a packet, so a single failure still shows.
function shareOf(part: number, total: number, count: number): number {
	if (part === 0) {
		return 0;
	}

	return Math.max(1, Math.round((part / total) * count));
}

function repeated(outcome: Outcome, times: number): readonly Outcome[] {
	return Array.from({ length: times }, () => outcome);
}
