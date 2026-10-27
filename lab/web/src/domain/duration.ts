import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';

const SECONDS_PER_MINUTE = 60;
const SECONDS_PER_HOUR = 3_600;

export function formatDuration(totalSeconds: number): string {
	const seconds = Math.max(0, Math.floor(totalSeconds));
	const hours = Math.floor(seconds / SECONDS_PER_HOUR);
	const minutes = Math.floor((seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE);
	const rest = seconds % SECONDS_PER_MINUTE;

	if (hours > 0) {
		return format(messages.duration.hours, { hours, minutes });
	}

	if (minutes > 0 && rest === 0) {
		return format(messages.duration.minutes, { minutes });
	}

	if (minutes > 0) {
		return format(messages.duration.minutesSeconds, { minutes, seconds: rest });
	}

	return format(messages.duration.seconds, { seconds: rest });
}

export function formatClock(totalSeconds: number): string {
	const seconds = Math.max(0, Math.floor(totalSeconds));
	const rest = String(seconds % SECONDS_PER_MINUTE).padStart(2, '0');

	return `${Math.floor(seconds / SECONDS_PER_MINUTE)}:${rest}`;
}
