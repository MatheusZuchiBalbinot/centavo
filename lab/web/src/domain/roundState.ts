import type { AuditorReading, Replay, RoundSecond } from '../api/replay';
import type { StreamMessage } from '../api/roundStream';

export type RoundState =
	| { readonly kind: 'connecting' }
	| {
			readonly kind: 'following' | 'finished';
			readonly replay: Replay;
			readonly reading: AuditorReading | null;
			// newEvents counts events that arrived after the snapshot, so a page
			// can announce them without repeating the history it opened with.
			readonly newEvents: number;
	  };

export const initialRoundState: RoundState = { kind: 'connecting' };

// A snapshot replaces everything, which is also how a reconnection catches up.
export function reduceRound(state: RoundState, message: StreamMessage): RoundState {
	switch (message.type) {
		case 'snapshot':
			return { kind: 'following', replay: message.replay, reading: readingOf(state), newEvents: 0 };
		case 'finished':
			return { kind: 'finished', replay: message.replay, reading: readingOf(state), newEvents: newEventsOf(state) };
		case 'seconds':
			return updateReplay(state, (replay) => ({ ...replay, seconds: mergeSeconds(replay.seconds, message.seconds) }));
		case 'event':
			return addEvent(state, message.event);
		case 'auditor':
			return state.kind === 'connecting' ? state : { ...state, reading: message.reading };
	}
}

// The stream resends the last second because it may still have grown.
export function mergeSeconds(known: readonly RoundSecond[], update: readonly RoundSecond[]): readonly RoundSecond[] {
	const first = update[0];

	if (first === undefined) {
		return known;
	}

	return [...known.filter((second) => second.t < first.t), ...update];
}

function addEvent(state: RoundState, event: Replay['events'][number]): RoundState {
	if (state.kind !== 'following') {
		return state;
	}

	return {
		...state,
		replay: { ...state.replay, events: [...state.replay.events, event] },
		newEvents: state.newEvents + 1,
	};
}

function updateReplay(state: RoundState, update: (replay: Replay) => Replay): RoundState {
	if (state.kind !== 'following') {
		return state;
	}

	return { ...state, replay: update(state.replay) };
}

function readingOf(state: RoundState): AuditorReading | null {
	return state.kind === 'connecting' ? null : state.reading;
}

function newEventsOf(state: RoundState): number {
	return state.kind === 'connecting' ? 0 : state.newEvents;
}
