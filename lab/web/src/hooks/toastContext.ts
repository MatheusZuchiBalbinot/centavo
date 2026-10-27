import { createContext, useContext } from 'react';

// error and broken interrupt (role="alert"); the others wait their turn.
type ToastTone = 'info' | 'attack' | 'error' | 'broken';

export type ToastInput = { readonly tone: ToastTone; readonly title: string; readonly detail?: string };

export type Toast = ToastInput & { readonly id: number };

export type ToastApi = {
	readonly toasts: readonly Toast[];
	readonly push: (toast: ToastInput) => void;
	readonly dismiss: (id: number) => void;
};

export const ToastContext = createContext<ToastApi | null>(null);

export function useToasts(): ToastApi {
	const api = useContext(ToastContext);

	if (api === null) {
		throw new Error('useToasts must be used inside ToastProvider');
	}

	return api;
}
