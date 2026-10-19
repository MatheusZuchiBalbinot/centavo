import { useCallback, useEffect, useRef, useState } from 'react';

import type { TermId } from '../i18n/glossary';
import { glossary } from '../i18n/glossary';

// What a tooltip shows: a short text, or a glossary entry.
export type TooltipContent =
  { readonly kind: 'text'; readonly text: string } | { readonly kind: 'term'; readonly term: TermId };

export type ActiveTooltip = {
  // SVG chart markers carry tooltips too.
  readonly anchor: HTMLElement | SVGElement;
  readonly rect: DOMRect;
  readonly content: TooltipContent;
};

export type TooltipControl = {
  readonly active: ActiveTooltip | null;
  // The bubble keeps itself open while the pointer is over it (WCAG 1.4.13).
  readonly holdOpen: () => void;
  readonly release: () => void;
};

const OPEN_DELAY_MS = 120;
const CLOSE_DELAY_MS = 160;
const ANCHOR_SELECTOR = '[data-tooltip], [data-term]';

// useActiveTooltip follows the pointer and the focus over the page and
// reports the element whose tooltip should show. Any element opts in with
// data-tooltip="text" or data-term="lease". Escape closes it; a tap on a
// term opens it on touch screens.
export function useActiveTooltip(): TooltipControl {
  const [active, setActive] = useState<ActiveTooltip | null>(null);
  const openTimer = useRef<number | undefined>(undefined);
  const closeTimer = useRef<number | undefined>(undefined);

  const holdOpen = useCallback(() => {
    window.clearTimeout(closeTimer.current);
  }, []);

  const release = useCallback(() => {
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setActive(null), CLOSE_DELAY_MS);
  }, []);

  useEffect(() => {
    function show(anchor: HTMLElement | SVGElement, delay: number): void {
      const content = contentOf(anchor);

      if (content === null) {
        return;
      }

      window.clearTimeout(closeTimer.current);
      window.clearTimeout(openTimer.current);
      openTimer.current = window.setTimeout(
        () => setActive({ anchor, rect: anchor.getBoundingClientRect(), content }),
        delay,
      );
    }

    function hide(): void {
      window.clearTimeout(openTimer.current);
      release();
    }

    function handlePointerOver(event: PointerEvent): void {
      const anchor = anchorOf(event.target);

      if (anchor) {
        show(anchor, OPEN_DELAY_MS);
      }
    }

    function handlePointerOut(event: PointerEvent): void {
      const anchor = anchorOf(event.target);
      const next = event.relatedTarget instanceof Node ? event.relatedTarget : null;
      const isLeaving = anchor !== null && !anchor.contains(next);

      if (isLeaving) {
        hide();
      }
    }

    function handleFocusIn(event: FocusEvent): void {
      const anchor = anchorOf(event.target);

      if (anchor) {
        show(anchor, 0);
      }
    }

    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key === 'Escape') {
        window.clearTimeout(openTimer.current);
        setActive(null);
      }
    }

    function handleScroll(): void {
      setActive(null);
    }

    document.addEventListener('pointerover', handlePointerOver);
    document.addEventListener('pointerout', handlePointerOut);
    document.addEventListener('focusin', handleFocusIn);
    document.addEventListener('focusout', hide);
    document.addEventListener('keydown', handleKeyDown);
    window.addEventListener('scroll', handleScroll, { passive: true, capture: true });

    return () => {
      window.clearTimeout(openTimer.current);
      window.clearTimeout(closeTimer.current);
      document.removeEventListener('pointerover', handlePointerOver);
      document.removeEventListener('pointerout', handlePointerOut);
      document.removeEventListener('focusin', handleFocusIn);
      document.removeEventListener('focusout', hide);
      document.removeEventListener('keydown', handleKeyDown);
      window.removeEventListener('scroll', handleScroll, { capture: true });
    };
  }, [release]);

  return { active, holdOpen, release };
}

function anchorOf(target: EventTarget | null): HTMLElement | SVGElement | null {
  if (!(target instanceof Element)) {
    return null;
  }

  const anchor = target.closest(ANCHOR_SELECTOR);
  const isAnchor = anchor instanceof HTMLElement || anchor instanceof SVGElement;

  return isAnchor ? anchor : null;
}

function contentOf(anchor: HTMLElement | SVGElement): TooltipContent | null {
  const term = anchor.dataset.term;

  if (term !== undefined && isTermId(term)) {
    return { kind: 'term', term };
  }

  const text = anchor.dataset.tooltip;

  return text ? { kind: 'text', text } : null;
}

function isTermId(value: string): value is TermId {
  return value in glossary;
}
