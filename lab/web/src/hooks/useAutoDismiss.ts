import { useEffect, useRef, useState } from 'react';

// useAutoDismiss calls dismiss after the delay, except while the pointer is
// over the element or focus is inside it; leaving starts the delay over, so
// the element never vanishes while someone reads or uses it.
export function useAutoDismiss<T extends HTMLElement>(delayMs: number, dismiss: () => void): React.RefObject<T | null> {
	const ref = useRef<T>(null);
	const [isPaused, setPaused] = useState(false);

	useEffect(() => {
		const element = ref.current;

		if (element === null) {
			return;
		}

		function pause(): void {
			setPaused(true);
		}

		function resume(): void {
			setPaused(false);
		}

		element.addEventListener('mouseenter', pause);
		element.addEventListener('mouseleave', resume);
		element.addEventListener('focusin', pause);
		element.addEventListener('focusout', resume);

		return () => {
			element.removeEventListener('mouseenter', pause);
			element.removeEventListener('mouseleave', resume);
			element.removeEventListener('focusin', pause);
			element.removeEventListener('focusout', resume);
		};
	}, []);

	useEffect(() => {
		if (isPaused) {
			return;
		}

		const timer = window.setTimeout(dismiss, delayMs);

		return () => {
			window.clearTimeout(timer);
		};
	}, [isPaused, delayMs, dismiss]);

	return ref;
}
