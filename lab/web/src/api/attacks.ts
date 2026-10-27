import { getJSON, type ApiResult } from './client';
import { isArrayOf, isNumber, isObject, isString } from './guards';
import { isAttackId, type AttackId } from './ids';
import { API_ROUTES } from './routes';

export type Attack = { readonly id: AttackId; readonly scenarios: readonly string[] };

export type AttackMenu = { readonly attacks: readonly Attack[]; readonly durationsSeconds: readonly number[] };

export function fetchAttackMenu(signal?: AbortSignal): Promise<ApiResult<AttackMenu>> {
	return getJSON(API_ROUTES.attacks, parseAttackMenu, signal);
}

function parseAttackMenu(body: unknown): AttackMenu | null {
	if (!isObject(body)) {
		return null;
	}

	const { attacks, durations_seconds: durationsSeconds } = body;

	const isMenu = isArrayOf(attacks, isAttack) && isArrayOf(durationsSeconds, isNumber);

	if (!isMenu) {
		return null;
	}

	return { attacks, durationsSeconds };
}

function isAttack(value: unknown): value is Attack {
	return isObject(value) && isAttackId(value.id) && isArrayOf(value.scenarios, isString);
}
