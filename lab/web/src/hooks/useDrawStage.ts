import { useEffect, useLayoutEffect, useRef } from 'react';

import { useNow } from './useNow';

export type DrawStage = { readonly kind: 'spinning' } | { readonly kind: 'counting'; readonly left: number } | { readonly kind: 'ready' };

const TICK_MS = 100;
const MS_PER_SECOND = 1_000;
export const COUNTDOWN_SECONDS = 3;

type DrawStageInput = {
	readonly startedAtMs: number;
	readonly spinMs: number;
	readonly onReady: () => void;
};

export function useDrawStage({ startedAtMs, spinMs, onReady }: DrawStageInput): DrawStage {
	const now = useNow(TICK_MS, true);
	const stage = stageAt(Math.max(0, now - startedAtMs), spinMs);
	const readyFor = useRef<number | null>(null);
	const onReadyRef = useRef(onReady);

	useLayoutEffect(() => {
		onReadyRef.current = onReady;
	});

	const isReady = stage.kind === 'ready';

	useEffect(() => {
		const hasFired = readyFor.current === startedAtMs;

		if (!isReady || hasFired) {
			return;
		}

		readyFor.current = startedAtMs;
		onReadyRef.current();
	}, [isReady, startedAtMs]);

	return stage;
}

export function stageAt(elapsedMs: number, spinMs: number): DrawStage {
	if (elapsedMs < spinMs) {
		return { kind: 'spinning' };
	}

	const counted = Math.floor((elapsedMs - spinMs) / MS_PER_SECOND);

	if (counted < COUNTDOWN_SECONDS) {
		return { kind: 'counting', left: COUNTDOWN_SECONDS - counted };
	}

	return { kind: 'ready' };
}
