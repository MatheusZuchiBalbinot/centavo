// The lab's system as the arena draws it: the processes and the links
// between them, named as the lab's events name them (deploy/compose and the
// Toxiproxy proxies).

export type NodeId = 'clients' | 'api' | 'worker1' | 'worker2' | 'postgres' | 'replica' | 'partner' | 'auditor';

export type LinkId =
  | 'client_api'
  | 'api_pg'
  | 'worker1_pg'
  | 'worker2_pg'
  | 'worker1_partner'
  | 'worker2_partner'
  | 'partner_webhooks'
  | 'replication'
  | 'auditor_pg';

// Where a node sits, as fractions of the map's width and height.
export type MapNode = { readonly id: NodeId; readonly x: number; readonly y: number; readonly service: string | null };

// bend pulls a link into an arc, as a fraction of the map's height, so it
// goes around the nodes between its ends instead of through them.
export type MapLink = { readonly id: LinkId; readonly from: NodeId; readonly to: NodeId; readonly bend: number };

// The requests flow left to right: clients, the API, the database, the
// workers and, last, the partner they call.
const CLIENTS_X = 0.08;
const API_X = 0.28;
const DATABASE_X = 0.5;
const WORKERS_X = 0.72;
const PARTNER_X = 0.92;
const TOP = 0.16;
const UPPER = 0.24;
const MIDDLE = 0.5;
const LOWER = 0.76;
const BOTTOM = 0.84;
// The webhooks come back from the partner to the API over everything else.
const WEBHOOK_BEND = -0.96;

export const MAP_NODES: readonly MapNode[] = [
  { id: 'clients', x: CLIENTS_X, y: MIDDLE, service: null },
  { id: 'api', x: API_X, y: MIDDLE, service: 'bank-api' },
  { id: 'auditor', x: DATABASE_X, y: TOP, service: null },
  { id: 'postgres', x: DATABASE_X, y: MIDDLE, service: 'postgres' },
  { id: 'replica', x: DATABASE_X, y: BOTTOM, service: 'postgres-replica' },
  { id: 'worker1', x: WORKERS_X, y: UPPER, service: 'bank-worker-1' },
  { id: 'worker2', x: WORKERS_X, y: LOWER, service: 'bank-worker-2' },
  { id: 'partner', x: PARTNER_X, y: MIDDLE, service: 'partner' },
];

// Each link points the way the requests go; the packets follow it.
export const MAP_LINKS: readonly MapLink[] = [
  { id: 'client_api', from: 'clients', to: 'api', bend: 0 },
  { id: 'api_pg', from: 'api', to: 'postgres', bend: 0 },
  { id: 'worker1_pg', from: 'worker1', to: 'postgres', bend: 0 },
  { id: 'worker2_pg', from: 'worker2', to: 'postgres', bend: 0 },
  { id: 'worker1_partner', from: 'worker1', to: 'partner', bend: 0 },
  { id: 'worker2_partner', from: 'worker2', to: 'partner', bend: 0 },
  { id: 'partner_webhooks', from: 'partner', to: 'api', bend: WEBHOOK_BEND },
  { id: 'replication', from: 'postgres', to: 'replica', bend: 0 },
  { id: 'auditor_pg', from: 'auditor', to: 'postgres', bend: 0 },
];

const NODE_BY_SERVICE: ReadonlyMap<string, NodeId> = new Map(
  MAP_NODES.flatMap((node) => (node.service === null ? [] : [[node.service, node.id] as const])),
);

const LINK_IDS: ReadonlySet<string> = new Set(MAP_LINKS.map((link) => link.id));

// nodesOf reads the nodes an event names: "bank-worker-1", or several joined
// by commas.
export function nodesOf(target: string): readonly NodeId[] {
  return namesOf(target).flatMap((name) => {
    const node = NODE_BY_SERVICE.get(name);

    return node === undefined ? [] : [node];
  });
}

// linksOf reads the links a network event names: "api_pg, worker1_pg".
export function linksOf(target: string): readonly LinkId[] {
  return namesOf(target).filter(isLinkId);
}

function namesOf(target: string): readonly string[] {
  return target
    .split(',')
    .map((name) => name.trim())
    .filter(Boolean);
}

function isLinkId(name: string): name is LinkId {
  return LINK_IDS.has(name);
}
