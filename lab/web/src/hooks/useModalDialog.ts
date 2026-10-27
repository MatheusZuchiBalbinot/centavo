import { useLayoutEffect } from 'react';

// Closes in a layout effect, which runs before React takes the dialog out
// of the page: closing a dialog still in the page is what lets the browser
// give the focus back to the button that opened it.
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
