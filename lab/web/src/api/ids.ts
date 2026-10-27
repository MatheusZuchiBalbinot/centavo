// Branded ids, so a round id and an attack id cannot be swapped.
export type RoundId = string & { readonly _brand: 'RoundId' };
export type AttackId = string & { readonly _brand: 'AttackId' };

const ROUND_ID_PATTERN = /^r\d{4,}$/;

export function isRoundId(value: unknown): value is RoundId {
	return typeof value === 'string' && ROUND_ID_PATTERN.test(value);
}

export function isAttackId(value: unknown): value is AttackId {
	return typeof value === 'string' && value !== '';
}
