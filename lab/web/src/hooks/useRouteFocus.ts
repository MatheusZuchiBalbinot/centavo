import { useEffect, useRef } from 'react';
import { useLocation } from 'react-router';

export const PAGE_HEADING_ID = 'page-heading';

// Moves focus to the new page's heading, so a keyboard or screen reader
// user starts at the top. The first render keeps the browser's own focus.
export function useRouteFocus(): void {
	const { pathname } = useLocation();
	const isFirstRender = useRef(true);

	useEffect(() => {
		if (isFirstRender.current) {
			isFirstRender.current = false;

			return;
		}

		document.getElementById(PAGE_HEADING_ID)?.focus();
	}, [pathname]);
}
