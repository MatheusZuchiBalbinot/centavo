import type { RoundEvent } from '../api/replay';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';
import { describeEvent } from './feed';
import { isAttackEvent, isDefenseEvent, isNetworkEnd } from './phase';
import { linksOf, nodesOf, type MapTarget } from './topology';

export type HistoryTone = 'attack' | 'defense' | 'state';

export type HistoryLine = { readonly key: string; readonly tMs: number; readonly text: string; readonly tone: HistoryTone };

const MAX_LINES = 8;

const statuses: Readonly<Partial<Record<string, string>>> = messages.arena.status;

// What happened to one process or link, newest first.
export function historyOf(target: MapTarget, events: readonly RoundEvent[]): readonly HistoryLine[] {
	const lines: HistoryLine[] = [];

	for (const [index, event] of events.entries()) {
		if (!isAbout(target, event)) {
			continue;
		}

		lines.push({ key: `${event.tMs}-${index}`, tMs: event.tMs, text: textOf(event), tone: toneOf(event) });
	}

	return lines.reverse().slice(0, MAX_LINES);
}

function isAbout(target: MapTarget, event: RoundEvent): boolean {
	if (target.kind === 'node') {
		return nodesOf(event.target).includes(target.id);
	}

	return linksOf(event.target).includes(target.id);
}

function textOf(event: RoundEvent): string {
	if (event.kind === 'process_state') {
		return format(messages.arena.inspector.state, { status: statuses[event.detail] ?? event.detail });
	}

	if (isNetworkEnd(event)) {
		return messages.arena.inspector.linkBack;
	}

	return describeEvent(event).what;
}

function toneOf(event: RoundEvent): HistoryTone {
	if (isDefenseEvent(event)) {
		return 'defense';
	}

	return isAttackEvent(event) ? 'attack' : 'state';
}
