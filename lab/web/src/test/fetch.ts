import { vi } from 'vitest';

// Answers fetch with one JSON body per path; a path not listed answers 404.
export type FetchAnswers = Readonly<Record<string, { readonly status: number; readonly body: unknown }>>;

export function answerFetch(answers: FetchAnswers): void {
  vi.spyOn(globalThis, 'fetch').mockImplementation(answer(answers));
}

function answer(answers: FetchAnswers): (input: RequestInfo | URL) => Promise<Response> {
  return (input) => {
    const path = pathOf(input);
    const found = answers[path] ?? { status: 404, body: { error: { code: 'not_found', message: 'none' } } };
    const headers = { 'Content-Type': 'application/json' };

    return Promise.resolve(new Response(JSON.stringify(found.body), { status: found.status, headers }));
  };
}

function pathOf(input: RequestInfo | URL): string {
  if (typeof input === 'string') {
    return input;
  }

  if (input instanceof URL) {
    return input.pathname;
  }

  return new URL(input.url).pathname;
}
