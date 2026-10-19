import { useLayoutEffect, useState } from 'react';

// Until the first measure, charts lay out at this width.
const FALLBACK_WIDTH = 640;

// useElementWidth follows an element's width, so a chart can draw in real
// pixels instead of scaling a fixed canvas (which shrinks its text).
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
