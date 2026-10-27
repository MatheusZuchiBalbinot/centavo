import { useLayoutEffect, useState } from 'react';

export type Size = { readonly width: number; readonly height: number };

const FALLBACK: Size = { width: 900, height: 520 };

export function useElementSize(ref: React.RefObject<HTMLElement | null>): Size {
	const [size, setSize] = useState<Size>(FALLBACK);

	useLayoutEffect(() => {
		const element = ref.current;

		if (element === null) {
			return;
		}

		const rect = element.getBoundingClientRect();

		if (rect.width > 0) {
			setSize({ width: rect.width, height: rect.height });
		}

		if (typeof ResizeObserver === 'undefined') {
			return;
		}

		const observer = new ResizeObserver((entries) => {
			const box = entries[0]?.contentRect;

			if (box && box.width > 0) {
				setSize({ width: box.width, height: box.height });
			}
		});

		observer.observe(element);

		return () => {
			observer.disconnect();
		};
	}, [ref]);

	return size;
}
