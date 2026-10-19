import type { RoundEvent } from '../api/replay';
import { isAttackEvent, isNetworkEnd } from './phase';
import { linksOf, MAP_LINKS, MAP_NODES, nodesOf } from './topology';
import type { LinkId, NodeId } from './topology';

// How long a hit or a defense stays lit on the map after it happened.
export const FLASH_MS = 2_500;

const MS_PER_SECOND = 1_000;

// What the map shows for a process.
export type NodeStatus = 'up' | 'starting' | 'frozen' | 'down';

export type NodeView = {
  readonly status: NodeStatus;
  // frozenUntilMs is set while a freeze with a known length lasts.
  readonly frozenUntilMs: number | null;
  readonly isHit: boolean;
  readonly isDefending: boolean;
  readonly isLying: boolean;
  readonly isLeader: boolean;
  readonly clockSkew: string | null;
  readonly lastEvent: RoundEvent | null;
};

export type LinkStatus = 'ok' | 'slow' | 'cut';

export type LinkView = { readonly status: LinkStatus; readonly toxic: string | null };

export type SystemState = {
  readonly nodes: Readonly<Record<NodeId, NodeView>>;
  readonly links: Readonly<Record<LinkId, LinkView>>;
  readonly isHealing: boolean;
};

// Toxics that stop traffic; the others only slow it.
const CUTTING_TOXICS: ReadonlySet<string> = new Set(['timeout', 'reset_peer', 'down', 'limit_data']);

// systemStateAt is the map at a moment of the round: what every event up to
// nowMs left behind. It is pure, so a replay can ask for any moment.
export function systemStateAt(events: readonly RoundEvent[], nowMs: number): SystemState {
  const nodes = new Map<NodeId, MutableNode>(MAP_NODES.map((node) => [node.id, freshNode(node.id)]));
  const links = new Map<LinkId, LinkView>(MAP_LINKS.map((link) => [link.id, { status: 'ok', toxic: null }]));
  let isHealing = false;

  for (const event of events) {
    if (event.tMs > nowMs) {
      break;
    }

    const isRecent = nowMs - event.tMs <= FLASH_MS;

    isHealing = applyEvent({ event, isRecent, nodes, links, nowMs }) ?? isHealing;
  }

  return {
    nodes: Object.fromEntries(nodes) as Record<NodeId, NodeView>,
    links: Object.fromEntries(links) as Record<LinkId, LinkView>,
    isHealing,
  };
}

type MutableNode = { -readonly [K in keyof NodeView]: NodeView[K] };

type Apply = {
  readonly event: RoundEvent;
  readonly isRecent: boolean;
  readonly nodes: Map<NodeId, MutableNode>;
  readonly links: Map<LinkId, LinkView>;
  readonly nowMs: number;
};

// applyEvent folds one event into the map; it returns the cure's state when
// the event changes it.
function applyEvent({ event, isRecent, nodes, links, nowMs }: Apply): boolean | undefined {
  const targets = nodesOf(event.target).flatMap((id) => nodes.get(id) ?? []);

  for (const node of targets) {
    node.lastEvent = event;

    if (isRecent && isAttackEvent(event)) {
      node.isHit = true;
    }

    if (isRecent && event.kind === 'defense') {
      node.isDefending = true;
    }
  }

  switch (event.kind) {
    case 'process_state':
      targets.forEach((node) => applyState(node, event.detail));

      return undefined;
    case 'freeze':
      targets.forEach((node) => applyFreeze(node, event, nowMs));

      return undefined;
    case 'skew_clock':
      targets.forEach((node) => (node.clockSkew = event.detail));

      return undefined;
    case 'kill':
      targets.forEach((node) => (node.status = 'down'));

      return undefined;
    case 'arm_partner_modes':
      markPartner(nodes, true);

      return undefined;
    case 'failover':
      moveLeader(nodes);

      return undefined;
    case 'network':
      applyNetwork(links, event);

      return undefined;
    case 'heal_start':
      healAll(nodes, links);

      return true;
    case 'heal_complete':
      bringAllUp(nodes);

      return false;
    case 'failpoint_fired':
    case 'database':
    case 'defense':
    case 'other':
      return undefined;
  }
}

function applyState(node: MutableNode, state: string): void {
  if (isNodeStatus(state)) {
    node.status = state;
  }

  if (state !== 'frozen') {
    node.frozenUntilMs = null;
  }
}

function applyFreeze(node: MutableNode, event: RoundEvent, nowMs: number): void {
  const seconds = Number.parseFloat(event.duration);
  const until = Number.isNaN(seconds) ? null : event.tMs + seconds * MS_PER_SECOND;

  if (until !== null && until <= nowMs) {
    return;
  }

  node.status = 'frozen';
  node.frozenUntilMs = until;
}

function applyNetwork(links: Map<LinkId, LinkView>, event: RoundEvent): void {
  const status: LinkStatus = isNetworkEnd(event) ? 'ok' : linkStatusOf(event.detail);

  for (const link of linksOf(event.target)) {
    links.set(link, { status, toxic: status === 'ok' ? null : event.detail });
  }
}

function linkStatusOf(toxic: string): LinkStatus {
  return CUTTING_TOXICS.has(toxic) ? 'cut' : 'slow';
}

function markPartner(nodes: Map<NodeId, MutableNode>, isLying: boolean): void {
  const partner = nodes.get('partner');

  if (partner) {
    partner.isLying = isLying;
  }
}

function moveLeader(nodes: Map<NodeId, MutableNode>): void {
  const primary = nodes.get('postgres');
  const replica = nodes.get('replica');

  if (primary && replica) {
    primary.isLeader = false;
    replica.isLeader = true;
  }
}

// healAll is the cure: faults off, clocks back, the partner honest again.
// Processes keep their last reported state; the lab reports them as they
// come back.
function healAll(nodes: Map<NodeId, MutableNode>, links: Map<LinkId, LinkView>): void {
  for (const node of nodes.values()) {
    node.clockSkew = null;
    node.isLying = false;
  }

  for (const id of links.keys()) {
    links.set(id, { status: 'ok', toxic: null });
  }
}

// bringAllUp ends the cure: it completes only once every process answers
// its health check again, and the lab stops watching them there.
function bringAllUp(nodes: Map<NodeId, MutableNode>): void {
  for (const node of nodes.values()) {
    node.status = 'up';
    node.frozenUntilMs = null;
  }
}

function freshNode(id: NodeId): MutableNode {
  return {
    status: 'up',
    frozenUntilMs: null,
    isHit: false,
    isDefending: false,
    isLying: false,
    isLeader: id === 'postgres',
    clockSkew: null,
    lastEvent: null,
  };
}

function isNodeStatus(value: string): value is NodeStatus {
  return value === 'up' || value === 'starting' || value === 'frozen' || value === 'down';
}
