import { getJSON, postJSON, type ApiResult } from './client';
import { isNumber, isObject, isString } from './guards';
import { isAttackId, isRoundId, type AttackId, type RoundId } from './ids';
import { API_ROUTES } from './routes';

export type LabState =
	| { readonly status: 'idle' }
	| {
			readonly status: 'running' | 'stopping';
			// round is null until the round has written its first file.
			readonly round: RoundId | null;
			readonly attack: AttackId;
			readonly scenario: string;
			readonly durationSeconds: number;
			readonly startedAt: Date;
	  };

type StartRoundInput = { readonly attack: AttackId; readonly durationSeconds: number };

export type ManualAction = 'kill' | 'freeze' | 'cut' | 'slow';

export type ManualAttack = { readonly action: ManualAction; readonly target: string };

const MANUAL_ACTIONS: ReadonlySet<string> = new Set<ManualAction>(['kill', 'freeze', 'cut', 'slow']);

export function fetchLabState(signal?: AbortSignal): Promise<ApiResult<LabState>> {
	return getJSON(API_ROUTES.lab, parseLabState, signal);
}

export function startRound(input: StartRoundInput): Promise<ApiResult<LabState>> {
	const body = { attack: input.attack, duration_seconds: input.durationSeconds };

	return postJSON(API_ROUTES.rounds, body, parseLabState);
}

export function stopRound(): Promise<ApiResult<LabState>> {
	return postJSON(API_ROUTES.stop, undefined, parseLabState);
}

// The lab answers 202 with the attack it passed on to the round.
export function launchManualAttack(attack: ManualAttack): Promise<ApiResult<ManualAttack>> {
	return postJSON(API_ROUTES.manualAttacks, attack, parseManualAttack);
}

export function parseLabState(body: unknown): LabState | null {
	if (!isObject(body)) {
		return null;
	}

	const { status, round, attack, scenario, duration_seconds: durationSeconds, started_at: startedAtText } = body;

	if (status === 'idle') {
		return { status };
	}

	const isRound = status === 'running' || status === 'stopping';
	const startedAt = isString(startedAtText) ? new Date(startedAtText) : null;
	const hasStart = startedAt !== null && !Number.isNaN(startedAt.getTime());

	const isComplete = isRound && isAttackId(attack) && isString(scenario) && isNumber(durationSeconds) && hasStart;

	if (!isComplete) {
		return null;
	}

	return { status, round: isRoundId(round) ? round : null, attack, scenario, durationSeconds, startedAt };
}

function parseManualAttack(body: unknown): ManualAttack | null {
	if (!isObject(body)) {
		return null;
	}

	const { action, target } = body;
	const isComplete = isManualAction(action) && isString(target);

	if (!isComplete) {
		return null;
	}

	return { action, target };
}

function isManualAction(value: unknown): value is ManualAction {
	return isString(value) && MANUAL_ACTIONS.has(value);
}
