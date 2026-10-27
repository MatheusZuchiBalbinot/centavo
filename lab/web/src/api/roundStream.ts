import { isObject } from './guards';
import type { RoundId } from './ids';
import {
	parseAuditorReading,
	parseEvent,
	parseReplay,
	parseSeconds,
	parseTransfers,
	type AuditorReading,
	type Replay,
	type RoundEvent,
	type RoundSecond,
	type RoundTransfer,
} from './replay';
import { roundEventsPath } from './routes';

// The messages a round stream sends, already checked: one snapshot, then
// updates, then finished.
export type StreamMessage =
	| { readonly type: 'snapshot'; readonly replay: Replay }
	| { readonly type: 'seconds'; readonly seconds: readonly RoundSecond[] }
	| { readonly type: 'event'; readonly event: RoundEvent }
	| { readonly type: 'transfers'; readonly transfers: readonly RoundTransfer[] }
	| { readonly type: 'auditor'; readonly reading: AuditorReading }
	| { readonly type: 'finished'; readonly replay: Replay };

type StreamHandlers = {
	readonly onMessage: (message: StreamMessage) => void;
	readonly onConnectionChange: (isConnected: boolean) => void;
	// onClosed fires when the browser gives up on the stream: the lab answered
	// with an error (a round that does not exist, too many streams) instead of
	// events, and EventSource never retries that.
	readonly onClosed: () => void;
};

const MESSAGE_TYPES = ['snapshot', 'seconds', 'event', 'transfers', 'auditor', 'finished'] as const;

// The browser reconnects by itself after a drop, and the server answers
// each connection with a new snapshot.
export function openRoundStream(round: RoundId, handlers: StreamHandlers): () => void {
	const source = new EventSource(roundEventsPath(round));

	function handle(event: MessageEvent<string>): void {
		const message = parseMessage(event.type, event.data);

		if (message === null) {
			return;
		}

		handlers.onConnectionChange(true);
		handlers.onMessage(message);

		// Finished is the last message; closing keeps the browser from
		// reconnecting to a round that is over.
		if (message.type === 'finished') {
			source.close();
		}
	}

	function handleError(): void {
		handlers.onConnectionChange(false);

		if (source.readyState === EventSource.CLOSED) {
			handlers.onClosed();
		}
	}

	for (const type of MESSAGE_TYPES) {
		source.addEventListener(type, handle);
	}

	source.addEventListener('error', handleError);

	return () => {
		source.close();
	};
}

function parseMessage(type: string, data: string): StreamMessage | null {
	const body = parseJSON(data);

	switch (type) {
		case 'snapshot':
			return replayMessage('snapshot', body);
		case 'finished':
			return replayMessage('finished', body);
		case 'seconds':
			return secondsMessage(body);
		case 'event':
			return eventMessage(body);
		case 'transfers':
			return transfersMessage(body);
		case 'auditor':
			return auditorMessage(body);
		default:
			return null;
	}
}

function replayMessage(type: 'snapshot' | 'finished', body: unknown): StreamMessage | null {
	const replay = parseReplay(body);

	return replay === null ? null : { type, replay };
}

function secondsMessage(body: unknown): StreamMessage | null {
	const seconds = isObject(body) ? parseSeconds(body.seconds) : null;

	return seconds === null ? null : { type: 'seconds', seconds };
}

function eventMessage(body: unknown): StreamMessage | null {
	const event = parseEvent(body);

	return event === null ? null : { type: 'event', event };
}

function transfersMessage(body: unknown): StreamMessage | null {
	const transfers = isObject(body) && Array.isArray(body.transfers) ? parseTransfers(body.transfers) : null;

	return transfers === null ? null : { type: 'transfers', transfers };
}

function auditorMessage(body: unknown): StreamMessage | null {
	const reading = parseAuditorReading(body);

	return reading === null ? null : { type: 'auditor', reading };
}

function parseJSON(data: string): unknown {
	try {
		return JSON.parse(data) as unknown;
	} catch {
		return null;
	}
}
