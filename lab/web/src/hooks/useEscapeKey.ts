import { useEffect, useLayoutEffect, useRef } from 'react';

// The callback is kept in a ref so the listener is attached once, not on
// every render.
export function useEscapeKey(isActive: boolean, onEscape: () => void): void {
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
				callback.current();
			}
		}

		document.addEventListener('keydown', handleKeyDown);

		return () => {
			document.removeEventListener('keydown', handleKeyDown);
		};
	}, [isActive]);
}
