import { useEffect, useLayoutEffect, useRef } from 'react';

// On a button or a slider inside the element, Space keeps its own meaning.
export function useSpaceKey(ref: React.RefObject<HTMLElement | null>, onSpace: (() => void) | undefined): void {
	const onSpaceRef = useRef(onSpace);
	const isEnabled = onSpace !== undefined;

	useLayoutEffect(() => {
		onSpaceRef.current = onSpace;
	});

	useEffect(() => {
		const element = ref.current;

		if (element === null || !isEnabled) {
			return;
		}

		function handleKeyDown(event: KeyboardEvent): void {
			const isSpaceOnElement = event.key === ' ' && event.target === element;

			if (!isSpaceOnElement) {
				return;
			}

			event.preventDefault();
			onSpaceRef.current?.();
		}

		element.addEventListener('keydown', handleKeyDown);

		return () => {
			element.removeEventListener('keydown', handleKeyDown);
		};
	}, [ref, isEnabled]);
}
