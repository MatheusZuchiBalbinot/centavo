import { useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

import { useActiveTooltip, type ActiveTooltip, type TooltipContent } from '../../hooks/useActiveTooltip';
import { format } from '../../i18n/format';
import { glossary } from '../../i18n/glossary';
import { messages } from '../../i18n/pt-BR';
import styles from './TooltipLayer.module.css';

const GAP_PX = 8;
const MARGIN_PX = 10;

type Placement = { readonly left: number; readonly top: number; readonly side: 'above' | 'below' };

// Portaled to the body so no card's overflow clips it. While open, the
// anchor points at it with aria-describedby, so a screen reader reads both.
export default function TooltipLayer(): React.JSX.Element | null {
	const { active, holdOpen, release } = useActiveTooltip();
	const id = useId();
	const bubble = useRef<HTMLDivElement>(null);
	const [placement, setPlacement] = useState<Placement | null>(null);

	useLayoutEffect(() => {
		if (active === null || bubble.current === null) {
			setPlacement(null);

			return;
		}

		setPlacement(placementOf(active, bubble.current.getBoundingClientRect()));

		const anchor = active.anchor;
		const previous = anchor.getAttribute('aria-describedby');

		anchor.setAttribute('aria-describedby', id);

		return () => {
			if (previous === null) {
				anchor.removeAttribute('aria-describedby');

				return;
			}

			anchor.setAttribute('aria-describedby', previous);
		};
	}, [active, id]);

	if (active === null) {
		return null;
	}

	return createPortal(
		<div
			ref={bubble}
			id={id}
			role="tooltip"
			className={styles.bubble}
			data-visible={placement !== null}
			data-side={placement?.side ?? 'below'}
			style={placement ? { left: placement.left, top: placement.top } : undefined}
			onPointerEnter={holdOpen}
			onPointerLeave={release}
		>
			<TooltipBody content={active.content} />
		</div>,
		document.body,
	);
}

function TooltipBody({ content }: { content: TooltipContent }): React.JSX.Element {
	if (content.kind === 'text') {
		return <p className={styles.text}>{content.text}</p>;
	}

	const entry = glossary[content.term];

	return (
		<>
			<p className={styles.term}>{entry.term}</p>
			<p className={styles.text}>{entry.text}</p>
			{entry.protects.length > 0 ? (
				<p className={styles.protects}>{format(messages.tooltip.protects, { guarantees: entry.protects.join(' · ') })}</p>
			) : null}
		</>
	);
}

function placementOf(active: ActiveTooltip, bubble: DOMRect): Placement {
	const { rect } = active;
	const centered = rect.left + rect.width / 2 - bubble.width / 2;
	const left = Math.min(Math.max(centered, MARGIN_PX), window.innerWidth - bubble.width - MARGIN_PX);
	const below = rect.bottom + GAP_PX;
	const hasRoomBelow = below + bubble.height <= window.innerHeight - MARGIN_PX;

	if (hasRoomBelow) {
		return { left, top: below, side: 'below' };
	}

	return { left, top: Math.max(MARGIN_PX, rect.top - GAP_PX - bubble.height), side: 'above' };
}
