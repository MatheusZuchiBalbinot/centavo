import { isObject, isString } from './guards';

// The codes the lab answers with, plus two the client itself detects.
type ApiErrorCode =
	| 'invalid_request'
	| 'unsupported_media_type'
	| 'body_too_large'
	| 'forbidden_origin'
	| 'rate_limited'
	| 'not_found'
	| 'round_running'
	| 'lab_idle'
	| 'unknown_attack'
	| 'invalid_duration'
	| 'too_many_streams'
	| 'internal'
	| 'network'
	| 'unexpected_response';

export type ApiFailure = { readonly code: ApiErrorCode; readonly status: number };

export type ApiResult<T> = { readonly ok: true; readonly data: T } | { readonly ok: false; readonly error: ApiFailure };

// parse turns the lab's JSON into the app's type, or null when it does not fit.
type Parser<T> = (body: unknown) => T | null;

const KNOWN_CODES: ReadonlySet<string> = new Set<ApiErrorCode>([
	'invalid_request',
	'unsupported_media_type',
	'body_too_large',
	'forbidden_origin',
	'rate_limited',
	'not_found',
	'round_running',
	'lab_idle',
	'unknown_attack',
	'invalid_duration',
	'too_many_streams',
	'internal',
]);

const NO_STATUS = 0;

export async function getJSON<T>(path: string, parse: Parser<T>, signal?: AbortSignal): Promise<ApiResult<T>> {
	const init: RequestInit = { headers: { Accept: 'application/json' }, cache: 'no-store' };

	if (signal) {
		init.signal = signal;
	}

	return request(path, init, parse);
}

export async function postJSON<T>(path: string, body: unknown, parse: Parser<T>): Promise<ApiResult<T>> {
	const init: RequestInit = {
		method: 'POST',
		headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
		body: body === undefined ? null : JSON.stringify(body),
	};

	return request(path, init, parse);
}

async function request<T>(path: string, init: RequestInit, parse: Parser<T>): Promise<ApiResult<T>> {
	const response = await fetchOrNull(path, init);

	if (response === null) {
		return failure('network', NO_STATUS);
	}

	const body = await readBody(response);

	if (!response.ok) {
		return failure(errorCodeOf(body), response.status);
	}

	const data = parse(body);

	if (data === null) {
		return failure('unexpected_response', response.status);
	}

	return { ok: true, data };
}

// fetchOrNull is fetch with a network failure as null instead of a throw.
async function fetchOrNull(path: string, init: RequestInit): Promise<Response | null> {
	try {
		return await fetch(path, init);
	} catch {
		return null;
	}
}

async function readBody(response: Response): Promise<unknown> {
	try {
		return (await response.json()) as unknown;
	} catch {
		return null;
	}
}

function errorCodeOf(body: unknown): ApiErrorCode {
	const error = isObject(body) ? body.error : undefined;
	const code = isObject(error) ? error.code : undefined;

	if (!isApiErrorCode(code)) {
		return 'unexpected_response';
	}

	return code;
}

function isApiErrorCode(value: unknown): value is ApiErrorCode {
	return isString(value) && KNOWN_CODES.has(value);
}

function failure<T>(code: ApiErrorCode, status: number): ApiResult<T> {
	return { ok: false, error: { code, status } };
}
