import { useEffect, useRef } from 'react';
import { useLocation } from 'react-router';

// The id of each page's h1; the layout moves focus there on navigation.
export const PAGE_HEADING_ID = 'page-heading';

// useRouteFocus moves focus to the new page's heading after a navigation,
// so a keyboard or screen reader user starts at the top of the new page.
// The first render keeps the browser's own focus.
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
