import { useEffect, useReducer, useState } from 'react';

import type { RoundId } from '../../../api/ids';
import { fetchReplay } from '../../../api/replay';
import { openRoundStream, type StreamMessage } from '../../../api/roundStream';
import { initialRoundState, reduceRound, type RoundState } from '../../../domain/roundState';

export type StreamAvailability = 'open' | 'missing' | 'unreachable';

type RoundStream = {
	readonly state: RoundState;
	readonly isConnected: boolean;
	readonly lastMessageAt: number | null;
	readonly availability: StreamAvailability;
	readonly retry: () => void;
};

// A stream the lab refused for a round that exists (too many streams, a
// restart) is opened again after this pause, never in a tight loop.
const REOPEN_DELAY_MS = 3_000;

export function useRoundStream(round: RoundId): RoundStream {
	const [state, dispatch] = useReducer(reduceRound, initialRoundState);
	const [isConnected, setConnected] = useState(true);
	const [lastMessageAt, setLastMessageAt] = useState<number | null>(null);
	const [availability, setAvailability] = useState<StreamAvailability>('open');
	const [attempt, setAttempt] = useState(0);

	useEffect(() => {
		const controller = new AbortController();
		let reopenTimer: number | undefined;

		function handleMessage(message: StreamMessage): void {
			setLastMessageAt(Date.now());
			dispatch(message);
		}

		function reopenLater(): void {
			reopenTimer = window.setTimeout(() => setAttempt((count) => count + 1), REOPEN_DELAY_MS);
		}

		// handleClosed asks whether the round exists, to tell a wrong link from
		// a lab that is not answering.
		async function handleClosed(): Promise<void> {
			const result = await fetchReplay(round, controller.signal);

			if (controller.signal.aborted) {
				return;
			}

			if (result.ok) {
				reopenLater();

				return;
			}

			setAvailability(result.error.code === 'not_found' ? 'missing' : 'unreachable');
		}

		function handleStreamClosed(): void {
			void handleClosed();
		}

		const handlers = { onMessage: handleMessage, onConnectionChange: setConnected, onClosed: handleStreamClosed };
		const close = openRoundStream(round, handlers);

		return () => {
			controller.abort();
			window.clearTimeout(reopenTimer);
			close();
		};
	}, [round, attempt]);

	function retry(): void {
		setAvailability('open');
		setAttempt((count) => count + 1);
	}

	return { state, isConnected, lastMessageAt, availability, retry };
}
