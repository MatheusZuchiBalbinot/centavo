import { useCallback, useMemo, useRef, useState } from 'react';

import { ToastContext, type Toast, type ToastApi, type ToastInput } from '../../hooks/toastContext';
import { Toaster } from './Toaster';

// Older toasts give way, so a burst of attacks never piles up on screen.
const MAX_TOASTS = 3;

type ToastProviderProps = { readonly children: React.ReactNode };

export function ToastProvider({ children }: ToastProviderProps): React.JSX.Element {
	const [toasts, setToasts] = useState<readonly Toast[]>([]);
	const nextId = useRef(1);

	const push = useCallback((toast: ToastInput) => {
		const id = nextId.current;
		nextId.current += 1;

		setToasts((current) => [...current, { ...toast, id }].slice(-MAX_TOASTS));
	}, []);

	const dismiss = useCallback((id: number) => {
		setToasts((current) => current.filter((toast) => toast.id !== id));
	}, []);

	// Consumers keep this object in effect dependencies; it only changes with
	// the list itself.
	const api = useMemo<ToastApi>(() => ({ toasts, push, dismiss }), [toasts, push, dismiss]);

	return (
		<ToastContext value={api}>
			{children}
			<Toaster toasts={toasts} onDismiss={dismiss} />
		</ToastContext>
	);
}
