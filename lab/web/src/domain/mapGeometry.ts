import { MAP_NODES, type MapLink, type NodeId } from './topology';

export type Point = { readonly x: number; readonly y: number };

export type MapFrame = { readonly width: number; readonly height: number; readonly isTurned: boolean };

export type LinkCurve = { readonly path: string; readonly middle: Point };

// Below this width the map turns, so the five columns become five rows.
const TURN_BELOW_PX = 640;
const HALF = 0.5;

export function frameOf(width: number, height: number): MapFrame {
	return { width, height, isTurned: width < TURN_BELOW_PX };
}

export function pointOf(id: NodeId, frame: MapFrame): Point {
	const node = MAP_NODES.find((candidate) => candidate.id === id);

	return placed(node?.x ?? 0, node?.y ?? 0, frame);
}

export function curveOf(link: MapLink, frame: MapFrame): LinkCurve {
	const from = pointOf(link.from, frame);
	const to = pointOf(link.to, frame);
	const control = bentMiddle(link, frame);
	const middle = {
		x: (from.x + to.x) / 4 + control.x / 2,
		y: (from.y + to.y) / 4 + control.y / 2,
	};

	return { path: `M ${from.x} ${from.y} Q ${control.x} ${control.y} ${to.x} ${to.y}`, middle };
}

function bentMiddle(link: MapLink, frame: MapFrame): Point {
	const from = MAP_NODES.find((node) => node.id === link.from);
	const to = MAP_NODES.find((node) => node.id === link.to);
	const across = ((from?.x ?? 0) + (to?.x ?? 0)) * HALF;
	const along = ((from?.y ?? 0) + (to?.y ?? 0)) * HALF + link.bend;

	return placed(across, along, frame);
}

function placed(x: number, y: number, frame: MapFrame): Point {
	if (frame.isTurned) {
		return { x: y * frame.width, y: x * frame.height };
	}

	return { x: x * frame.width, y: y * frame.height };
}
