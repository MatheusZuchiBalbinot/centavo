import { describe, expect, it } from 'vitest';

import { splitTerms } from './terms';

describe('splitTerms', () => {
	it('marks each term once, at its first appearance, and keeps the rest as text', () => {
		const segments = splitTerms('O lease vence; o outro worker pega o lease. Sem fencing, o zumbi grava.');

		expect(segments).toEqual([
			{ kind: 'text', text: 'O ' },
			{ kind: 'term', term: 'lease', text: 'lease' },
			{ kind: 'text', text: ' vence; o outro worker pega o lease. Sem ' },
			{ kind: 'term', term: 'fencing', text: 'fencing' },
			{ kind: 'text', text: ', o zumbi grava.' },
		]);
	});

	it('prefers the longest form and ignores words that only contain a term', () => {
		const segments = splitTerms('A chave de idempotência protege; leasekeeper não é termo.');

		expect(segments[1]).toEqual({ kind: 'term', term: 'idempotencyKey', text: 'chave de idempotência' });
		expect(segments.filter((segment) => segment.kind === 'term')).toHaveLength(1);
	});

	it('leaves a text without terms whole', () => {
		expect(splitTerms('Nada aqui.')).toEqual([{ kind: 'text', text: 'Nada aqui.' }]);
	});
});
