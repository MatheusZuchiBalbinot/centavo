import { useLayoutEffect, useState } from 'react';

const FALLBACK_WIDTH = 640;

// Draws in real pixels instead of scaling a fixed canvas, which shrinks
// its text.
export function useElementWidth(ref: React.RefObject<HTMLElement | null>): number {
	const [width, setWidth] = useState(FALLBACK_WIDTH);

	useLayoutEffect(() => {
		const element = ref.current;

		if (element === null) {
			return;
		}

		setWidth(element.getBoundingClientRect().width || FALLBACK_WIDTH);

		if (typeof ResizeObserver === 'undefined') {
			return;
		}

		const observer = new ResizeObserver((entries) => {
			const measured = entries[0]?.contentRect.width;

			if (measured) {
				setWidth(measured);
			}
		});

		observer.observe(element);

		return () => {
			observer.disconnect();
		};
	}, [ref]);

	return width;
}
