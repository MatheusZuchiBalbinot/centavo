import type { LabReading } from '../hooks/useLabState';
import { messages } from '../i18n/pt-BR';
import { LiveIndicator, type LiveState } from './ui/LiveIndicator';

type LabStatusProps = { readonly reading: LabReading };

// LabStatus is the top bar's word on the lab: free, running a round live,
// stopping one, or out of reach.
export default function LabStatus({ reading }: LabStatusProps): React.JSX.Element {
	const { state, label } = describe(reading);

	return (
		<span role="status">
			<LiveIndicator state={state} label={label} />
		</span>
	);
}

function describe(reading: LabReading): { state: LiveState; label: string } {
	switch (reading.kind) {
		case 'loading':
			return { state: 'waiting', label: messages.labStatus.unknown };
		case 'offline':
			return { state: 'offline', label: messages.labStatus.offline };
		case 'ready':
			return describeStatus(reading.state.status);
	}
}

function describeStatus(status: 'idle' | 'running' | 'stopping'): { state: LiveState; label: string } {
	switch (status) {
		case 'idle':
			return { state: 'idle', label: messages.labStatus.idle };
		case 'running':
			return { state: 'live', label: messages.labStatus.running };
		case 'stopping':
			return { state: 'waiting', label: messages.labStatus.stopping };
	}
}
