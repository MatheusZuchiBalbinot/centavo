import { useEffect, useLayoutEffect, useRef } from 'react';

import type { AttackRange } from '../domain/highlight';

// useHoverRange reports a range while the pointer or the focus is on the
// element, and null when it leaves. The listeners are attached once; the
// latest callback is read from a ref.
export function useHoverRange(
  ref: React.RefObject<Element | null>,
  range: AttackRange | null,
  onHighlight: (range: AttackRange | null) => void,
): void {
  const latest = useRef({ range, onHighlight });

  useLayoutEffect(() => {
    latest.current = { range, onHighlight };
  }, [range, onHighlight]);

  useEffect(() => {
    const element = ref.current;

    if (element === null) {
      return;
    }

    function enter(): void {
      latest.current.onHighlight(latest.current.range);
    }

    function leave(): void {
      latest.current.onHighlight(null);
    }

    element.addEventListener('pointerenter', enter);
    element.addEventListener('pointerleave', leave);
    element.addEventListener('focusin', enter);
    element.addEventListener('focusout', leave);

    return () => {
      element.removeEventListener('pointerenter', enter);
      element.removeEventListener('pointerleave', leave);
      element.removeEventListener('focusin', enter);
      element.removeEventListener('focusout', leave);
    };
  }, [ref]);
}
