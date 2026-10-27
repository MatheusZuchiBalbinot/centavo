import { useOutletContext } from 'react-router';

import type { LabReading } from './useLabState';

// useLabReading is the lab state the layout polls, shared with every page so
// the panel asks the lab once, not once per component.
export function useLabReading(): LabReading {
	return useOutletContext<LabReading>();
}
