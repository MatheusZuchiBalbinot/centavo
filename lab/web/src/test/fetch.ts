import { vi } from 'vitest';

// Answers fetch with one JSON body per path, ignoring the query string. A key
// can name the method too ("POST /api/rounds"), which wins over the bare
// path; a path not listed answers 404.
export type FetchAnswers = Readonly<Record<string, { readonly status: number; readonly body: unknown }>>;

const NOT_FOUND = { status: 404, body: { error: { code: 'not_found', message: 'none' } } };

export function answerFetch(answers: FetchAnswers): void {
	vi.spyOn(globalThis, 'fetch').mockImplementation(answer(answers));
}

function answer(answers: FetchAnswers): (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> {
	return (input, init) => {
		const path = pathOf(input);
		const method = init?.method ?? 'GET';
		const found = answers[`${method} ${path}`] ?? answers[path] ?? NOT_FOUND;
		const headers = { 'Content-Type': 'application/json' };

		return Promise.resolve(new Response(JSON.stringify(found.body), { status: found.status, headers }));
	};
}

function pathOf(input: RequestInfo | URL): string {
	if (typeof input === 'string') {
		return new URL(input, 'http://panel.test').pathname;
	}

	if (input instanceof URL) {
		return input.pathname;
	}

	return new URL(input.url).pathname;
}
