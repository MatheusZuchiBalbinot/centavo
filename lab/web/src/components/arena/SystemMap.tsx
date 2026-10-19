import { Activity, Database, DatabaseBackup, Handshake, Search, Server, Users } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useRef } from 'react';

import type { RoundEvent } from '../../api/replay';
import { describeEvent } from '../../domain/feed';
import { curveOf, frameOf, pointOf } from '../../domain/mapGeometry';
import type { MapFrame } from '../../domain/mapGeometry';
import type { LinkView, NodeView, SystemState } from '../../domain/systemState';
import { linksOf, MAP_LINKS, MAP_NODES, nodesOf } from '../../domain/topology';
import type { MapLink, MapNode, NodeId } from '../../domain/topology';
import { useElementSize } from '../../hooks/useElementSize';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';

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

const ICON_SIZE = 18;
const MS_PER_SECOND = 1_000;
const HALF = 0.5;

// Packets per link: more on the busy links, never more than this many.
const MAX_PACKETS = 3;
const OPERATIONS_PER_PACKET = 8;

// Links that carry the clients' traffic, whose packets follow its rate.
const BUSY_LINKS: ReadonlySet<string> = new Set(['client_api', 'api_pg']);

type SystemMapProps = {
  readonly state: SystemState;
  readonly operationsPerSecond: number;
  readonly bubble: { readonly event: RoundEvent; readonly label: string } | null;
  readonly nowMs: number;
  readonly isLive: boolean;
};

// SystemMap draws the lab's system and what the round does to it right now.
export default function SystemMap({
  state,
  operationsPerSecond,
  bubble,
  nowMs,
  isLive,
}: SystemMapProps): React.JSX.Element {
  const box = useRef<HTMLDivElement>(null);
  const size = useElementSize(box);
  const layout = frameOf(size.width, size.height);

  return (
    <div ref={box} className={styles.map} role="group" aria-label={messages.arena.mapLabel}>
      <svg className={styles.links} width={size.width} height={size.height} aria-hidden="true">
        {MAP_LINKS.map((link) => (
          <LinkLine key={link.id} link={link} view={state.links[link.id]} frame={layout} />
        ))}
      </svg>
      {isLive ? <Packets state={state} frame={layout} operationsPerSecond={operationsPerSecond} /> : null}
      {MAP_LINKS.map((link) => (
        <LinkLabel key={link.id} link={link} view={state.links[link.id]} frame={layout} />
      ))}
      {MAP_NODES.map((node) => (
        <NodeCard
          key={node.id}
          node={node}
          view={state.nodes[node.id]}
          frame={layout}
          nowMs={nowMs}
          operationsPerSecond={operationsPerSecond}
        />
      ))}
      {bubble ? <AttackBubble event={bubble.event} label={bubble.label} frame={layout} /> : null}
    </div>
  );
}

type LinkPartProps = { readonly link: MapLink; readonly view: LinkView; readonly frame: MapFrame };

function LinkLine({ link, view, frame }: LinkPartProps): React.JSX.Element {
  return <path className={styles.link} data-status={view.status} d={curveOf(link, frame).path} />;
}

function LinkLabel({ link, view, frame }: LinkPartProps): React.JSX.Element | null {
  if (view.status === 'ok' || view.toxic === null) {
    return null;
  }

  const { middle } = curveOf(link, frame);
  const text = format(view.status === 'cut' ? messages.arena.linkCut : messages.arena.linkSlow, { toxic: view.toxic });

  return (
    <span className={styles.linkLabel} data-status={view.status} style={{ left: middle.x, top: middle.y }}>
      {text}
    </span>
  );
}

type PacketsProps = { readonly state: SystemState; readonly frame: MapFrame; readonly operationsPerSecond: number };

// Packets run along the links that carry traffic, slower on a slow link and
// not at all on a cut one or out of a process that is down.
function Packets({ state, frame, operationsPerSecond }: PacketsProps): React.JSX.Element {
  const packets = MAP_LINKS.flatMap((link) => packetsOf(link, state, frame, operationsPerSecond));

  return (
    <div className={styles.packets} aria-hidden="true">
      {packets.map((packet) => (
        <span key={packet.key} className={styles.packet} data-slow={packet.isSlow} style={packet.style} />
      ))}
    </div>
  );
}

type Packet = { readonly key: string; readonly isSlow: boolean; readonly style: React.CSSProperties };

function packetsOf(link: MapLink, state: SystemState, frame: MapFrame, operationsPerSecond: number): readonly Packet[] {
  const view = state.links[link.id];
  const isSourceAlive = state.nodes[link.from].status === 'up' && state.nodes[link.to].status !== 'down';
  const isFlowing = view.status !== 'cut' && isSourceAlive;

  if (!isFlowing) {
    return [];
  }

  const count = BUSY_LINKS.has(link.id)
    ? Math.min(MAX_PACKETS, Math.ceil(operationsPerSecond / OPERATIONS_PER_PACKET))
    : 1;
  const { path } = curveOf(link, frame);
  const isSlow = view.status === 'slow';

  return Array.from({ length: count }, (_, index) => ({
    key: `${link.id}-${index}`,
    isSlow,
    style: {
      offsetPath: `path('${path}')`,
      animationDelay: `${(index * 0.55).toFixed(2)}s`,
    },
  }));
}

type NodeCardProps = {
  readonly node: MapNode;
  readonly view: NodeView;
  readonly frame: MapFrame;
  readonly nowMs: number;
  readonly operationsPerSecond: number;
};

function NodeCard({ node, view, frame, nowMs, operationsPerSecond }: NodeCardProps): React.JSX.Element {
  const Icon = ICONS[node.id];
  const name = messages.arena.nodes[node.id];
  const center = pointOf(node.id, frame);
  const status = statusText(node.id, view, nowMs, operationsPerSecond);
  const help =
    view.lastEvent === null
      ? messages.arena.nodeHelp[node.id]
      : `${messages.arena.nodeHelp[node.id]} ${describeEvent(view.lastEvent).what}`;

  return (
    <button
      type="button"
      className={styles.node}
      data-status={view.status}
      data-hit={view.isHit}
      data-defending={view.isDefending}
      style={{ left: center.x, top: center.y }}
      data-tooltip={help}
      aria-label={`${name}: ${status}`}
    >
      <span className={styles.nodeIcon} aria-hidden="true">
        <Icon size={ICON_SIZE} />
      </span>
      <span className={styles.nodeText}>
        <span className={styles.nodeName}>{name}</span>
        <span className={styles.nodeStatus}>{status}</span>
      </span>
      <NodeBadges view={view} />
    </button>
  );
}

function NodeBadges({ view }: { view: NodeView }): React.JSX.Element | null {
  const badges = [
    view.isLeader ? { key: 'leader', text: messages.arena.leader, tone: 'info' } : null,
    view.isLying ? { key: 'lying', text: messages.arena.lying, tone: 'violet' } : null,
    view.clockSkew
      ? { key: 'skew', text: format(messages.arena.skew, { offset: view.clockSkew }), tone: 'warning' }
      : null,
    view.isDefending ? { key: 'defending', text: messages.arena.defending, tone: 'success' } : null,
  ].filter((badge) => badge !== null);

  if (badges.length === 0) {
    return null;
  }

  return (
    <span className={styles.nodeBadges}>
      {badges.map((badge) => (
        <span key={badge.key} className={styles.nodeBadge} data-tone={badge.tone}>
          {badge.text}
        </span>
      ))}
    </span>
  );
}

function statusText(id: NodeId, view: NodeView, nowMs: number, operationsPerSecond: number): string {
  if (id === 'clients') {
    return format(messages.arena.clientsRate, { rate: Math.round(operationsPerSecond) });
  }

  if (view.status === 'frozen' && view.frozenUntilMs !== null) {
    return format(messages.arena.frozenFor, {
      seconds: Math.max(0, Math.ceil((view.frozenUntilMs - nowMs) / MS_PER_SECOND)),
    });
  }

  return messages.arena.status[view.status];
}

type AttackBubbleProps = { readonly event: RoundEvent; readonly label: string; readonly frame: MapFrame };

// AttackBubble points at the node the newest attack hit, on the side with
// more room.
function AttackBubble({ event, label, frame }: AttackBubbleProps): React.JSX.Element {
  const { what, expected } = describeEvent(event);
  const anchor = pointOf(anchorOf(event), frame);
  const isOnRight = anchor.x < frame.width * HALF;

  return (
    <div className={styles.bubble} data-side={isOnRight ? 'right' : 'left'} style={{ left: anchor.x, top: anchor.y }}>
      <strong>{format(messages.arena.bubble, { label })}</strong>
      <span>{what}</span>
      {expected === '' ? null : <small>{expected}</small>}
    </div>
  );
}

// anchorOf is the node an attack points at: the process it hit, or where the
// link it slowed or cut starts; the API when it names neither.
function anchorOf(event: RoundEvent): NodeId {
  const node = nodesOf(event.target)[0];

  if (node !== undefined) {
    return node;
  }

  const link = MAP_LINKS.find((candidate) => candidate.id === linksOf(event.target)[0]);

  return link?.from ?? 'api';
}
