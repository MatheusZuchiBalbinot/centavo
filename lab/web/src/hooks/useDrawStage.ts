import { useEffect, useLayoutEffect, useRef } from 'react';

import { useNow } from './useNow';

// Where a surprise draw is: the reel spinning, the 3-2-1, or ready to start.
export type DrawStage = { readonly kind: 'spinning' } | { readonly kind: 'counting'; readonly left: number } | { readonly kind: 'ready' };

const TICK_MS = 100;
const MS_PER_SECOND = 1_000;
export const COUNTDOWN_SECONDS = 3;

type DrawStageInput = {
	// startedAtMs is when this draw began; a new draw starts over.
	readonly startedAtMs: number;
	readonly spinMs: number;
	readonly onReady: () => void;
};

// useDrawStage follows a draw by the clock and calls onReady once, when the
// countdown ends.
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
