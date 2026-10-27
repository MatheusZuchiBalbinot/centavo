import { vi } from 'vitest';

// FakeEventSource stands in for the browser's, which jsdom lacks; a test
// sends it messages as the lab would.
export class FakeEventSource extends EventTarget {
  static last: FakeEventSource | null = null;
  static readonly CONNECTING = 0;
  static readonly CLOSED = 2;

  readonly url: string;
  isClosed = false;
  readyState = FakeEventSource.CONNECTING;

  constructor(url: string) {
    super();
    this.url = url;
    FakeEventSource.last = this;
  }

  send(type: string, data: unknown): void {
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }));
  }

  // refuse is the lab answering with an error instead of events: the
  // browser closes the stream for good and reports one error.
  refuse(): void {
    this.readyState = FakeEventSource.CLOSED;
    this.dispatchEvent(new Event('error'));
  }

  close(): void {
    this.isClosed = true;
    this.readyState = FakeEventSource.CLOSED;
  }
}

export function installFakeEventSource(): void {
  FakeEventSource.last = null;
  vi.stubGlobal('EventSource', FakeEventSource);
}
