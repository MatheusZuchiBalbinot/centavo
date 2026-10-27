import { useEffect, useRef, useState } from 'react';

import type { Verdict } from '../api/rounds';
import { playCue, unlockAudio } from '../audio/cues';
import { readStored, STORAGE_KEYS, writeStored } from '../storage';

// At 60x a replay lands attacks faster than a cue can be told apart.
const MIN_ATTACK_GAP_MS = 250;

type ArenaSound = { readonly isOn: boolean; readonly toggle: () => void };

type ArenaSoundInput = { readonly attacks: number; readonly verdict: Verdict | null };

export function useArenaSound({ attacks, verdict }: ArenaSoundInput): ArenaSound {
	const [isOn, setOn] = useState(() => readStored(STORAGE_KEYS.sound, isBoolean) ?? false);
	const heard = useRef({ attacks, verdict, lastAttackAt: 0 });

	useEffect(() => {
		const previous = heard.current;
		const hasNewAttack = attacks > previous.attacks;
		const hasNewVerdict = verdict !== null && previous.verdict === null;

		heard.current = { ...previous, attacks, verdict };

		if (!isOn) {
			return;
		}

		if (hasNewVerdict) {
			playCue(verdict === 'fail' ? 'fail' : 'pass');

			return;
		}

		const now = performance.now();
		const isSpaced = now - previous.lastAttackAt >= MIN_ATTACK_GAP_MS;

		if (hasNewAttack && isSpaced) {
			heard.current = { ...heard.current, lastAttackAt: now };
			playCue('attack');
		}
	}, [attacks, verdict, isOn]);

	function toggle(): void {
		const next = !isOn;

		if (next) {
			unlockAudio();
		}

		setOn(next);
		writeStored(STORAGE_KEYS.sound, next);
	}

	return { isOn, toggle };
}

function isBoolean(value: unknown): value is boolean {
	return typeof value === 'boolean';
}
