import { useRef } from 'react';

import { Button } from '../../../components/ui/Button';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';

type StopRoundDialogProps = { readonly isStopping: boolean; readonly onConfirm: () => void };

// StopRoundDialog asks before stopping, in a native modal dialog: it traps
// focus, closes on Escape and gives focus back to the button that opened it.
export default function StopRoundDialog({ isStopping, onConfirm }: StopRoundDialogProps): React.JSX.Element {
	const dialog = useRef<HTMLDialogElement>(null);

	function handleOpen(): void {
		dialog.current?.showModal();
	}

	function handleCancel(): void {
		dialog.current?.close();
	}

	function handleConfirm(): void {
		dialog.current?.close();
		onConfirm();
	}

	return (
		<>
			<Button variant="danger" onClick={handleOpen} disabled={isStopping}>
				{messages.round.stop}
			</Button>
			<dialog ref={dialog} className={styles.dialog} aria-labelledby="stop-title" aria-describedby="stop-detail">
				<h2 id="stop-title" className={styles.dialogTitle}>
					{messages.round.stopTitle}
				</h2>
				<p id="stop-detail">{messages.round.stopDetail}</p>
				<div className={styles.dialogActions}>
					{/* The safe choice comes first: showModal focuses it on open. */}
					<Button variant="primary" onClick={handleCancel}>
						{messages.round.stopCancel}
					</Button>
					<Button variant="danger" onClick={handleConfirm}>
						{messages.round.stopConfirm}
					</Button>
				</div>
			</dialog>
		</>
	);
}
