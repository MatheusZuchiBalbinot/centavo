import { Pause, Play } from 'lucide-react';

import { formatClock } from '../../domain/duration';
import { PLAYBACK_SPEEDS } from '../../hooks/usePlayback';
import type { Playback, PlaybackSpeed } from '../../hooks/usePlayback';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { Button } from '../ui/Button';
import styles from './Arena.module.css';

const MS_PER_SECOND = 1_000;
// The scrubber moves a second at a time with the arrow keys.
const STEP_MS = 1_000;

type ReplayPlayerProps = { readonly playback: Playback; readonly endMs: number };

// ReplayPlayer plays a recorded round in the arena: play and pause, the
// speed, and a bar to drag to any moment.
export default function ReplayPlayer({ playback, endMs }: ReplayPlayerProps): React.JSX.Element {
  const at = formatClock(playback.positionMs / MS_PER_SECOND);
  const total = formatClock(endMs / MS_PER_SECOND);

  function handleSeek(event: React.ChangeEvent<HTMLInputElement>): void {
    playback.seek(Number(event.target.value));
  }

  return (
    <div className={styles.player} role="group" aria-label={messages.arena.player.label}>
      <Button
        onClick={playback.toggle}
        aria-label={playback.isPlaying ? messages.arena.player.pause : messages.arena.player.play}
      >
        {playback.isPlaying ? <Pause size={16} aria-hidden="true" /> : <Play size={16} aria-hidden="true" />}
      </Button>
      <input
        className={styles.scrubber}
        type="range"
        min={0}
        max={endMs}
        step={STEP_MS}
        value={playback.positionMs}
        onChange={handleSeek}
        aria-label={messages.arena.player.position}
        aria-valuetext={format(messages.arena.player.positionText, { at, total })}
      />
      <span className={styles.playerClock}>
        {at} / {total}
      </span>
      <div className={styles.speeds} role="group" aria-label={messages.arena.player.speeds}>
        {PLAYBACK_SPEEDS.map((speed) => (
          <SpeedButton key={speed} speed={speed} isOn={playback.speed === speed} onSelect={playback.setSpeed} />
        ))}
      </div>
    </div>
  );
}

type SpeedButtonProps = {
  readonly speed: PlaybackSpeed;
  readonly isOn: boolean;
  readonly onSelect: (speed: PlaybackSpeed) => void;
};

function SpeedButton({ speed, isOn, onSelect }: SpeedButtonProps): React.JSX.Element {
  return (
    <button type="button" className={styles.speed} aria-pressed={isOn} onClick={() => onSelect(speed)}>
      {format(messages.arena.player.speed, { speed })}
    </button>
  );
}
