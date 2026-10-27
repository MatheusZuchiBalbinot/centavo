import type { RoundEvent } from '../api/replay';
import { messages } from '../i18n/pt-BR';
import { describeEvent } from './feed';
import { isAttackEvent, type Phase } from './phase';

const ATTACK_WINDOW_MS = 15_000;

export type Narration = { readonly text: string; readonly isAttack: boolean };

// The arena's headline: the attack of the last few seconds, or what the round
// is doing between attacks.
export function narrate(events: readonly RoundEvent[], nowMs: number, phase: Phase): Narration {
	const attack = events.findLast(isAttackBy(nowMs));
	const isRecent = attack !== undefined && nowMs - attack.tMs <= ATTACK_WINDOW_MS;

	if (!isRecent) {
		return { text: messages.arena.phaseNow[phase], isAttack: false };
	}

	return { text: describeEvent(attack).what, isAttack: true };
}

function isAttackBy(nowMs: number): (event: RoundEvent) => boolean {
	return (event) => event.tMs <= nowMs && isAttackEvent(event);
}
