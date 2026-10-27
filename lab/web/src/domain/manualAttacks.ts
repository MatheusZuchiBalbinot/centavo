import type { ManualAction, ManualAttack } from '../api/lab';
import type { SystemState } from './systemState';
import { MAP_NODES, type LinkId, type MapTarget, type NodeId } from './topology';

export type ManualFeedback = { readonly tone: 'sent' | 'refused'; readonly text: string };

// Sent attacks cool down for a moment, so a double click is one attack.
export type ManualAttackState =
	| { readonly status: 'ready'; readonly feedback: ManualFeedback | null }
	| { readonly status: 'sending' }
	| { readonly status: 'cooling'; readonly feedback: ManualFeedback };

export type ManualAttackControl = { readonly state: ManualAttackState; readonly launch: (attack: ManualAttack) => void };

// The lab takes the same targets: the auditor, the replica and their links
// are the lab's own view of the bank, and the clients are the load itself.
const NODE_ACTIONS: readonly ManualAction[] = ['kill', 'freeze'];
const LINK_ACTIONS: readonly ManualAction[] = ['cut', 'slow'];
const ATTACKABLE_NODES: ReadonlySet<NodeId> = new Set(['api', 'worker1', 'worker2', 'postgres', 'partner']);
const ATTACKABLE_LINKS: ReadonlySet<LinkId> = new Set([
	'client_api',
	'api_pg',
	'worker1_pg',
	'worker2_pg',
	'worker1_partner',
	'worker2_partner',
	'partner_webhooks',
]);

export function manualActionsOf(target: MapTarget): readonly ManualAction[] {
	if (target.kind === 'node') {
		return ATTACKABLE_NODES.has(target.id) ? NODE_ACTIONS : [];
	}

	return ATTACKABLE_LINKS.has(target.id) ? LINK_ACTIONS : [];
}

export function manualAttackOf(target: MapTarget, action: ManualAction): ManualAttack | null {
	if (!manualActionsOf(target).includes(action)) {
		return null;
	}

	if (target.kind === 'link') {
		return { action, target: target.id };
	}

	const service = MAP_NODES.find((node) => node.id === target.id)?.service ?? null;

	return service === null ? null : { action, target: service };
}

// A fault on a target already down or broken would only stack on itself.
export function canAttackNow(target: MapTarget, state: SystemState): boolean {
	if (target.kind === 'node') {
		return state.nodes[target.id].status === 'up';
	}

	return state.links[target.id].status === 'ok';
}
