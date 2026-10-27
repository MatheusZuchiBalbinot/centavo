import { useEffect, useState } from 'react';

export const PLAYBACK_SPEEDS = [1, 10, 30, 60] as const;

export type PlaybackSpeed = (typeof PLAYBACK_SPEEDS)[number];

export type Playback = {
	readonly positionMs: number;
	readonly speed: PlaybackSpeed;
	readonly isPlaying: boolean;
	readonly toggle: () => void;
	readonly seek: (positionMs: number) => void;
	readonly setSpeed: (speed: PlaybackSpeed) => void;
};

type PlaybackInput = {
	readonly endMs: number;
	readonly startMs: number;
	readonly speed: PlaybackSpeed;
	readonly isLooping: boolean;
	readonly holdMs: number;
};

type PlaybackState = { readonly positionMs: number; readonly speed: PlaybackSpeed; readonly isPlaying: boolean };

const TICK_MS = 100;

export function usePlayback(input: PlaybackInput): Playback {
	const { endMs, isLooping, holdMs } = input;
	const [state, setState] = useState<PlaybackState>({
		positionMs: Math.min(input.startMs, endMs),
		speed: input.speed,
		isPlaying: true,
	});

	useEffect(() => {
		if (!state.isPlaying) {
			return;
		}

		let last = performance.now();

		function tick(): void {
			const now = performance.now();
			const realMs = now - last;

			last = now;
			setState((current) => advance(current, realMs, { endMs, isLooping, holdMs }));
		}

		const timer = window.setInterval(tick, TICK_MS);

		return () => {
			window.clearInterval(timer);
		};
	}, [state.isPlaying, endMs, isLooping, holdMs]);

	function toggle(): void {
		setState((current) => restartIfOver({ ...current, isPlaying: !current.isPlaying }, endMs));
	}

	function seek(positionMs: number): void {
		setState((current) => ({ ...current, positionMs: Math.max(0, Math.min(positionMs, endMs)) }));
	}

	function setSpeed(speed: PlaybackSpeed): void {
		setState((current) => ({ ...current, speed }));
	}

	return {
		positionMs: Math.min(state.positionMs, endMs),
		speed: state.speed,
		isPlaying: state.isPlaying,
		toggle,
		seek,
		setSpeed,
	};
}

type Bounds = { readonly endMs: number; readonly isLooping: boolean; readonly holdMs: number };

export function advance(state: PlaybackState, realMs: number, bounds: Bounds): PlaybackState {
	const positionMs = state.positionMs + realMs * state.speed;

	if (!bounds.isLooping) {
		const isOver = positionMs >= bounds.endMs;

		return { ...state, positionMs: Math.min(positionMs, bounds.endMs), isPlaying: !isOver };
	}

	// While it rests on the end the clock runs at real speed, so the hold
	// lasts holdMs whatever the playback speed.
	const isResting = state.positionMs >= bounds.endMs;

	if (!isResting) {
		return { ...state, positionMs: Math.min(positionMs, bounds.endMs) };
	}

	const restedMs = state.positionMs + realMs;

	return { ...state, positionMs: restedMs >= bounds.endMs + bounds.holdMs ? 0 : restedMs };
}

function restartIfOver(state: PlaybackState, endMs: number): PlaybackState {
	const isOver = state.positionMs >= endMs;

	return state.isPlaying && isOver ? { ...state, positionMs: 0 } : state;
}
