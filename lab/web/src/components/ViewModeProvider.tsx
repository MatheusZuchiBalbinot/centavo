import { useCallback, useMemo, useState } from 'react';

import { isViewMode, ViewModeContext, type ViewMode, type ViewModeApi } from '../hooks/viewModeContext';
import { readStored, STORAGE_KEYS, writeStored } from '../storage';

type ViewModeProviderProps = { readonly children: React.ReactNode };

export function ViewModeProvider({ children }: ViewModeProviderProps): React.JSX.Element {
	const [mode, setModeState] = useState<ViewMode>(() => readStored(STORAGE_KEYS.viewMode, isViewMode) ?? 'explained');

	const setMode = useCallback((next: ViewMode) => {
		setModeState(next);
		writeStored(STORAGE_KEYS.viewMode, next);
	}, []);

	const api = useMemo<ViewModeApi>(() => ({ mode, setMode }), [mode, setMode]);

	return <ViewModeContext value={api}>{children}</ViewModeContext>;
}
