import { useLayoutEffect } from 'react';

// useModalDialog shows a <dialog> as a modal for as long as the component is
// mounted. It closes it in a layout effect, which runs before React takes
// the dialog out of the page: closing a dialog still in the page is what
// lets the browser put the focus back on the button that opened it.
export function useModalDialog(ref: React.RefObject<HTMLDialogElement | null>): void {
	useLayoutEffect(() => {
		const element = ref.current;

		if (element === null || element.open) {
			return;
		}

		element.showModal();

		return () => {
			element.close();
		};
	}, [ref]);
}
