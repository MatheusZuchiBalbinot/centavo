import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import styles from './Composer.module.css';

const SECONDS_PER_MINUTE = 60;

type DurationPickerProps = {
  readonly durationsSeconds: readonly number[];
  readonly selected: number;
  readonly isDisabled: boolean;
  readonly onSelect: (seconds: number) => void;
};

// DurationPicker is a native radio group drawn as one segmented track.
export default function DurationPicker({
  durationsSeconds,
  selected,
  isDisabled,
  onSelect,
}: DurationPickerProps): React.JSX.Element {
  return (
    <div className={styles.segmented}>
      {durationsSeconds.map((seconds) => (
        <label key={seconds} className={styles.segment} data-checked={seconds === selected}>
          <input
            type="radio"
            name="duration"
            className={styles.radioInput}
            value={seconds}
            checked={seconds === selected}
            disabled={isDisabled}
            onChange={() => onSelect(seconds)}
          />
          {format(messages.composer.minutes, { minutes: seconds / SECONDS_PER_MINUTE })}
        </label>
      ))}
    </div>
  );
}
