import { createContext, useContext } from 'react';

// How the panel tells what happens: in plain words, with the technical
// details, or as a big screen for a demo.
export type ViewMode = 'explained' | 'technical' | 'presentation';

export const VIEW_MODES: readonly ViewMode[] = ['explained', 'technical', 'presentation'];

export type ViewModeApi = { readonly mode: ViewMode; readonly setMode: (mode: ViewMode) => void };

export const ViewModeContext = createContext<ViewModeApi>({ mode: 'explained', setMode: () => undefined });

export function useViewMode(): ViewModeApi {
  return useContext(ViewModeContext);
}

export function isViewMode(value: unknown): value is ViewMode {
  return value === 'explained' || value === 'technical' || value === 'presentation';
}
