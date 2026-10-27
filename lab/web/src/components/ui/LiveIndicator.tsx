export type LiveState = 'live' | 'waiting' | 'offline' | 'idle';

type LiveIndicatorProps = {
	readonly state: LiveState;
	readonly label: string;
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
