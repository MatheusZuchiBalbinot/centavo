import { useEffect, useLayoutEffect, useRef } from 'react';

// The callback is kept in a ref so the listener is attached once, not on
// every render. It gets the event, so it can keep Esc from also closing the
// dialog around it.
export function useEscapeKey(isActive: boolean, onEscape: (event: KeyboardEvent) => void): void {
	const callback = useRef(onEscape);

	useLayoutEffect(() => {
		callback.current = onEscape;
	}, [onEscape]);

	useEffect(() => {
		if (!isActive) {
			return;
		}

		function handleKeyDown(event: KeyboardEvent): void {
			if (event.key === 'Escape') {
				callback.current(event);
			}
		}

		document.addEventListener('keydown', handleKeyDown);

		return () => {
			document.removeEventListener('keydown', handleKeyDown);
		};
	}, [isActive]);
}
