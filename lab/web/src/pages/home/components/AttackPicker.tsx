import type { Attack } from '../../../api/attacks';
import type { AttackId } from '../../../api/ids';
import { attackCopy } from '../../../domain/attacks';
import AttackIcon from './AttackIcon';
import styles from './Composer.module.css';

type AttackPickerProps = {
  readonly attacks: readonly Attack[];
  readonly selected: AttackId;
  readonly isDisabled: boolean;
  readonly onSelect: (attack: AttackId) => void;
};

// AttackPicker is a native radio group drawn as cards: arrow keys move
// between attacks, and the group is one Tab stop.
export default function AttackPicker({
  attacks,
  selected,
  isDisabled,
  onSelect,
}: AttackPickerProps): React.JSX.Element {
  return (
    <div className={styles.attacks}>
      {attacks.map((attack) => (
        <AttackCard
          key={attack.id}
          attack={attack.id}
          isChecked={attack.id === selected}
          isDisabled={isDisabled}
          onSelect={onSelect}
        />
      ))}
    </div>
  );
}

type AttackCardProps = {
  readonly attack: AttackId;
  readonly isChecked: boolean;
  readonly isDisabled: boolean;
  readonly onSelect: (attack: AttackId) => void;
};

function AttackCard({ attack, isChecked, isDisabled, onSelect }: AttackCardProps): React.JSX.Element {
  const copy = attackCopy(attack);
  const detailId = `attack-${attack}-detail`;

  return (
    <label className={styles.attack} data-checked={isChecked}>
      <input
        type="radio"
        name="attack"
        className={styles.radioInput}
        value={attack}
        checked={isChecked}
        disabled={isDisabled}
        aria-describedby={detailId}
        onChange={() => onSelect(attack)}
      />
      <AttackIcon attack={attack} className={styles.attackIcon} />
      <span className={styles.attackName}>{copy.name}</span>
      <span id={detailId} className={styles.attackDetail}>
        {copy.detail}
      </span>
      <span className={styles.attackFamily}>{copy.family}</span>
    </label>
  );
}
