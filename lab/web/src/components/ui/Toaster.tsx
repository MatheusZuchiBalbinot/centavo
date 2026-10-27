import { useCallback } from 'react';

import type { Toast } from '../../hooks/toastContext';
import { useAutoDismiss } from '../../hooks/useAutoDismiss';
import { useEscapeKey } from '../../hooks/useEscapeKey';
import { messages } from '../../i18n/pt-BR';
import styles from './Toaster.module.css';

const TOAST_LIFETIME_MS = 8_000;

type ToasterProps = { readonly toasts: readonly Toast[]; readonly onDismiss: (id: number) => void };

// Toaster keeps both live regions on the page from the start, since a region
// added together with its message is not announced.
export function Toaster({ toasts, onDismiss }: ToasterProps): React.JSX.Element {
	const urgent = toasts.filter(isUrgent);
	const calm = toasts.filter(isCalm);
	const newest = toasts.at(-1);

	function dismissNewest(): void {
		if (newest) {
			onDismiss(newest.id);
		}
	}

	useEscapeKey(newest !== undefined, dismissNewest);

	return (
		<section className={styles.toaster} aria-label={messages.toasts.region}>
			<div role="status">
				<ol className={styles.list}>
					{calm.map((toast) => (
						<ToastItem key={toast.id} toast={toast} onDismiss={onDismiss} />
					))}
				</ol>
			</div>
			<div role="alert">
				<ol className={styles.list}>
					{urgent.map((toast) => (
						<ToastItem key={toast.id} toast={toast} onDismiss={onDismiss} />
					))}
				</ol>
			</div>
		</section>
	);
}

type ToastItemProps = { readonly toast: Toast; readonly onDismiss: (id: number) => void };

function ToastItem({ toast, onDismiss }: ToastItemProps): React.JSX.Element {
	const { id } = toast;
	const dismiss = useCallback(() => {
		onDismiss(id);
	}, [id, onDismiss]);
	const ref = useAutoDismiss<HTMLLIElement>(TOAST_LIFETIME_MS, dismiss);

	return (
		<li ref={ref} className={styles.toast} data-tone={toast.tone}>
			<span className={styles.icon} aria-hidden="true">
				!
			</span>
			<strong className={styles.title}>{toast.title}</strong>
			{toast.detail ? <p className={styles.detail}>{toast.detail}</p> : null}
			<button type="button" className={styles.close} aria-label={messages.toasts.close} onClick={dismiss}>
				<span aria-hidden="true">✕</span>
			</button>
		</li>
	);
}

function isUrgent(toast: Toast): boolean {
	return toast.tone === 'error' || toast.tone === 'broken';
}

function isCalm(toast: Toast): boolean {
	return !isUrgent(toast);
}
