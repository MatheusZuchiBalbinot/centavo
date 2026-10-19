import { vi } from 'vitest';

// FakeEventSource stands in for the browser's, which jsdom lacks; a test
// sends it messages as the lab would.
export class FakeEventSource extends EventTarget {
  static last: FakeEventSource | null = null;

  readonly url: string;
  isClosed = false;

  constructor(url: string) {
    super();
    this.url = url;
    FakeEventSource.last = this;
  }

  send(type: string, data: unknown): void {
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }));
  }

  close(): void {
    this.isClosed = true;
  }
}

export function installFakeEventSource(): void {
  FakeEventSource.last = null;
  vi.stubGlobal('EventSource', FakeEventSource);
}
