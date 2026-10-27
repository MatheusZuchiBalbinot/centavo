import type { AuditorReading, GuaranteeCheck } from '../api/replay';
import { messages } from '../i18n/pt-BR';

export type GuaranteeId = 'I1' | 'I2' | 'I3' | 'I4' | 'I5' | 'I6' | 'I7' | 'I8';

export const GUARANTEE_IDS: readonly GuaranteeId[] = ['I1', 'I2', 'I3', 'I4', 'I5', 'I6', 'I7', 'I8'];

export type GuaranteeView =
	| { readonly kind: 'final'; readonly status: GuaranteeCheck['status']; readonly violations: number }
	| { readonly kind: 'live'; readonly isHolding: boolean }
	| { readonly kind: 'later' };

// Before the checker's verdict, I1 and I4 follow the auditor, which reads
// the money live; the others wait for the end.
export function guaranteeView(id: GuaranteeId, checks: readonly GuaranteeCheck[], reading: AuditorReading | null): GuaranteeView {
	const final = checks.find((check) => check.id === id);

	if (final) {
		return { kind: 'final', status: final.status, violations: final.violations };
	}

	const isReadable = reading !== null && !reading.isUnavailable;

	if (isReadable && id === 'I1') {
		return { kind: 'live', isHolding: reading.sumCents === 0 };
	}

	if (isReadable && id === 'I4') {
		return { kind: 'live', isHolding: (reading.minCustomerCents ?? 0) >= 0 };
	}

	return { kind: 'later' };
}

export function guaranteeCopy(id: GuaranteeId): {
	name: string;
	short: string;
	detail: string;
	why: string;
	threats: string;
} {
	return messages.guarantees[id];
}

export function firstBroken(checks: readonly GuaranteeCheck[]): GuaranteeId | null {
	const broken = new Set(checks.filter(isFailed).map(idOf));

	return GUARANTEE_IDS.find((id) => broken.has(id)) ?? null;
}

export function brokenCount(checks: readonly GuaranteeCheck[]): number {
	return checks.filter(isFailed).length;
}

function isFailed(check: GuaranteeCheck): boolean {
	return check.status === 'failed';
}

function idOf(check: GuaranteeCheck): string {
	return check.id;
}
