import type { AuditorReading, Replay } from '../api/replay';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';
import { attacksText } from './attacks';
import { formatDuration } from './duration';
import { firstBroken, guaranteeCopy } from './guarantees';
import { isAttackEvent, type Phase } from './phase';

// The defense profile is "robust", optionally followed by the defenses the
// lab turned off: "robust,-fencing".
const PROFILE_SEPARATOR = ',';
const DISABLED_PREFIX = '-';

type HeadlineInput = {
	readonly replay: Replay;
	readonly phase: Phase;
	readonly elapsedSeconds: number;
	readonly reading: AuditorReading | null;
};

// headlineOf is the round's first line: what is happening now, or how it
// ended.
export function headlineOf(input: HeadlineInput): string {
	const { replay, phase } = input;

	switch (phase) {
		case 'attack':
			return format(messages.round.headlineAttack, {
				elapsed: formatDuration(input.elapsedSeconds),
				money: moneyText(input.reading),
			});
		case 'heal':
			return messages.round.headlineHeal;
		case 'check':
			return messages.round.headlineCheck;
		case 'verdict':
			return verdictHeadline(replay);
	}
}

// replayHeadlineOf is the first line of a replay: the recording keeps no
// auditor readings, so under attack it only says how long it has run.
export function replayHeadlineOf(input: Omit<HeadlineInput, 'reading'>): string {
	if (input.phase === 'attack') {
		return format(messages.arena.replayAttack, { elapsed: formatDuration(input.elapsedSeconds) });
	}

	return headlineOf({ ...input, reading: null });
}

// disabledDefenses lists the defenses the profile turned off.
export function disabledDefenses(defenses: string): readonly string[] {
	return defenses
		.split(PROFILE_SEPARATOR)
		.slice(1)
		.filter((change) => change.startsWith(DISABLED_PREFIX))
		.map((change) => change.slice(DISABLED_PREFIX.length));
}

// expectationOf says what the defense profile should make of the round.
export function expectationOf(defenses: string): string {
	if (defenses === '') {
		return messages.round.expectUnknown;
	}

	const disabled = disabledDefenses(defenses);

	if (disabled.length === 0) {
		return messages.round.expectRobust;
	}

	return format(messages.round.expectWithout, { defenses: disabled.join(', ') });
}

function moneyText(reading: AuditorReading | null): string {
	const isUnread = reading === null || reading.isUnavailable;

	if (isUnread) {
		return messages.round.moneyUnknown;
	}

	return reading.sumCents === 0 ? messages.round.moneyOk : messages.round.moneyBroken;
}

function verdictHeadline(replay: Replay): string {
	const attacks = replay.events.filter(isAttackEvent).length;

	switch (replay.verdict) {
		case 'pass':
			return attacks === 0 ? messages.round.headlinePassCalm : format(messages.round.headlinePass, { attacks: attacksText(attacks) });
		case 'fail':
			return format(messages.round.headlineFail, { guarantee: brokenName(replay) });
		case 'inconclusive':
			return messages.round.headlineInconclusive;
		case null:
			return messages.round.headlineError;
	}
}

function brokenName(replay: Replay): string {
	const broken = firstBroken(replay.checks);

	return broken === null ? messages.verdict.fail : guaranteeCopy(broken).name.toLowerCase();
}
