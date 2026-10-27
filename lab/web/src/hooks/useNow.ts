import { useEffect, useState } from 'react';

export function useNow(intervalMs: number, isTicking: boolean): number {
	const [now, setNow] = useState(() => Date.now());

	useEffect(() => {
		if (!isTicking) {
			return;
		}

		const timer = window.setInterval(() => setNow(Date.now()), intervalMs);

		return () => {
			window.clearInterval(timer);
		};
	}, [intervalMs, isTicking]);

	return now;
}
