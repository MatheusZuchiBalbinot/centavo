// What a live indicator says: data arriving now, waiting for it, the lab
// out of reach, or nothing running.
export type LiveState = 'live' | 'waiting' | 'offline' | 'idle';

type LiveIndicatorProps = {
	readonly state: LiveState;
	readonly label: string;
	// age says how fresh the data is, "há 2 s".
	readonly age?: string;
};

export function LiveIndicator({ state, label, age }: LiveIndicatorProps): React.JSX.Element {
	return (
		<span className="live-indicator" data-state={state}>
			<span className="live-indicator-dot" aria-hidden="true" />
			{label}
			{age === undefined ? null : <span className="live-indicator-age">· {age}</span>}
		</span>
	);
}
