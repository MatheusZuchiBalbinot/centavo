import { isObject } from './guards';
import type { RoundId } from './ids';
import { parseAuditorReading, parseEvent, parseReplay, parseSeconds } from './replay';
import type { AuditorReading, Replay, RoundEvent, RoundSecond } from './replay';
import { roundEventsPath } from './routes';

// The messages a round stream sends, already checked: one snapshot, then
// updates, then finished.
export type StreamMessage =
  | { readonly type: 'snapshot'; readonly replay: Replay }
  | { readonly type: 'seconds'; readonly seconds: readonly RoundSecond[] }
  | { readonly type: 'event'; readonly event: RoundEvent }
  | { readonly type: 'auditor'; readonly reading: AuditorReading }
  | { readonly type: 'finished'; readonly replay: Replay };

export type StreamHandlers = {
  readonly onMessage: (message: StreamMessage) => void;
  readonly onConnectionChange: (isConnected: boolean) => void;
};

const MESSAGE_TYPES = ['snapshot', 'seconds', 'event', 'auditor', 'finished'] as const;

// openRoundStream follows a round until it finishes; the browser reconnects
// by itself after a drop, and the server answers each connection with a new
// snapshot. It returns the function that closes the stream.
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
  }

  for (const type of MESSAGE_TYPES) {
    source.addEventListener(type, handle);
  }

  source.addEventListener('error', handleError);

  return () => {
    source.close();
  };
}

export function parseMessage(type: string, data: string): StreamMessage | null {
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
