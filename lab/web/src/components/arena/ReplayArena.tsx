import type { Replay } from '../../api/replay';
import { replayHeadlineOf } from '../../domain/headline';
import { endOfMs, playbackPhase, replayAt } from '../../domain/playback';
import { usePlayback, type PlaybackSpeed } from '../../hooks/usePlayback';
import Arena from './Arena';
import ReplayPlayer from './ReplayPlayer';

const MS_PER_SECOND = 1_000;

export type ReplayArenaProps = {
	readonly eyebrow: string;
	readonly replay: Replay;
	readonly startMs: number;
	readonly speed: PlaybackSpeed;
	readonly isLooping: boolean;
	readonly holdMs: number;
	readonly wallMs: number;
	readonly actions?: React.ReactNode;
	readonly onPause?: (positionMs: number) => void;
	readonly onClose: () => void;
};

export default function ReplayArena(props: ReplayArenaProps): React.JSX.Element {
	const { eyebrow, replay, startMs, speed, isLooping, holdMs, wallMs, actions, onPause, onClose } = props;
	const endMs = endOfMs(replay);
	const playback = usePlayback({ endMs, startMs, speed, isLooping, holdMs });
	const shown = replayAt(replay, playback.positionMs);
	const phase = playbackPhase(replay, playback.positionMs);
	const headlineInput = { replay: shown, phase, elapsedSeconds: playback.positionMs / MS_PER_SECOND };

	function handleToggle(): void {
		if (playback.isPlaying) {
			onPause?.(playback.positionMs);
		}

		playback.toggle();
	}

	function handleWatchAgain(): void {
		playback.seek(0);

		if (!playback.isPlaying) {
			playback.toggle();
		}
	}

	const controls = { ...playback, toggle: handleToggle };

	return (
		<Arena
			eyebrow={eyebrow}
			title={replayHeadlineOf(headlineInput)}
			replay={shown}
			reading={null}
			phase={phase}
			nowMs={playback.positionMs}
			ageSeconds={null}
			wallMs={wallMs}
			betLine={null}
			actions={actions}
			isLive={false}
			player={<ReplayPlayer playback={controls} endMs={endMs} />}
			onWatchAgain={handleWatchAgain}
			onSpace={handleToggle}
			onClose={onClose}
		/>
	);
}
