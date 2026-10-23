import { useEffect, useLayoutEffect, useRef } from 'react';

// useSpaceKey calls onSpace when Space is pressed while the element itself
// has focus; on a button or a slider inside it, Space keeps its own meaning.
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
