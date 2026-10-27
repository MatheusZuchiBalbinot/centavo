import type { RoundSecond } from '../api/replay';

// What the clients' operations ended in during one second.
export type Traffic = { readonly ok: number; readonly fail: number; readonly info: number };

const NO_TRAFFIC: Traffic = { ok: 0, fail: 0, info: 0 };

// While the round runs, its newest second is still filling up and would show
// a fraction of the load.
export function trafficAt(seconds: readonly RoundSecond[], nowSeconds: number, isFilling: boolean): Traffic {
	const settled = isFilling ? seconds.slice(0, -1) : seconds;
	const latest = settled.findLast((second) => second.t <= nowSeconds) ?? settled.at(-1);

	return latest ?? NO_TRAFFIC;
}

export function totalOf(traffic: Traffic): number {
	return traffic.ok + traffic.fail + traffic.info;
}
