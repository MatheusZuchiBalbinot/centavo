import { describe, expect, it, vi } from 'vitest';

import { answerFetch } from '../test/fetch';
import { getJSON } from './client';
import { isObject } from './guards';

function parseAnything(body: unknown): unknown {
	return isObject(body) ? body : null;
}

describe('getJSON', () => {
	it('returns the parsed body', async () => {
		answerFetch({ '/test/x': { status: 200, body: { a: 1 } } });

		expect(await getJSON('/test/x', parseAnything)).toEqual({ ok: true, data: { a: 1 } });
	});

	it('returns the code the lab answered with', async () => {
		answerFetch({ '/test/x': { status: 409, body: { error: { code: 'round_running', message: 'busy' } } } });

		expect(await getJSON('/test/x', parseAnything)).toEqual({
			ok: false,
			error: { code: 'round_running', status: 409 },
		});
	});

	it('never trusts a code it does not know', async () => {
		answerFetch({ '/test/x': { status: 500, body: { error: { code: 'drop_tables', message: '?' } } } });

		expect(await getJSON('/test/x', parseAnything)).toEqual({
			ok: false,
			error: { code: 'unexpected_response', status: 500 },
		});
	});

	it('turns a body that does not fit into an error', async () => {
		answerFetch({ '/test/x': { status: 200, body: [1, 2] } });

		expect(await getJSON('/test/x', parseAnything)).toEqual({
			ok: false,
			error: { code: 'unexpected_response', status: 200 },
		});
	});

	it('reports a network failure without throwing', async () => {
		vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('offline'));

		expect(await getJSON('/test/x', parseAnything)).toEqual({ ok: false, error: { code: 'network', status: 0 } });
	});
});
