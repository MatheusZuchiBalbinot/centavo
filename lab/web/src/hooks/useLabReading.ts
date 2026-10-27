import { useOutletContext } from 'react-router';

import type { LabReading } from './useLabState';

// Shared with every page, so the panel asks the lab once instead of once
// per component.
export function useLabReading(): LabReading {
	return useOutletContext<LabReading>();
}
