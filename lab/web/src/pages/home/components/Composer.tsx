import { useState } from 'react';

import type { AttackMenu } from '../../../api/attacks';
import type { AttackId } from '../../../api/ids';
import { Button } from '../../../components/ui/Button';
import { Sheet } from '../../../components/ui/Sheet';
import { messages } from '../../../i18n/pt-BR';
import type { StartChoice } from '../hooks/useStartRound';
import AttackPicker from './AttackPicker';
import styles from './Composer.module.css';
import DurationPicker from './DurationPicker';
import RoundPreview from './RoundPreview';

// The duration the menu opens on: long enough to see the attacks land.
const PREFERRED_DURATION_SECONDS = 300;

type ComposerProps = {
  readonly menu: AttackMenu;
  readonly isBusy: boolean;
  readonly isStarting: boolean;
  readonly onStart: (choice: StartChoice) => void;
};

export default function Composer({ menu, isBusy, isStarting, onStart }: ComposerProps): React.JSX.Element {
  const [attack, setAttack] = useState<AttackId | null>(null);
  const [durationSeconds, setDurationSeconds] = useState<number | null>(null);

  const selectedAttack = attack ?? firstAttackOf(menu);
  const selectedDuration = durationSeconds ?? preferredDurationOf(menu);
  const isLocked = isBusy || isStarting;

  function handleSubmit(event: React.SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();

    if (selectedAttack === null || isLocked) {
      return;
    }

    onStart({ attack: selectedAttack, durationSeconds: selectedDuration });
  }

  if (selectedAttack === null) {
    return <p role="alert">{messages.composer.menuFailed}</p>;
  }

  return (
    <Sheet as="section" className={styles.composer} aria-labelledby="composer-title">
      <form className={styles.form} onSubmit={handleSubmit}>
        <div className={styles.steps}>
          <h2 id="composer-title" className="visually-hidden">
            {messages.composer.title}
          </h2>
          <fieldset className={styles.step}>
            <legend className={styles.stepTitle}>
              <span className={styles.stepNumber}>1</span>
              {messages.composer.attackStep}
            </legend>
            <AttackPicker attacks={menu.attacks} selected={selectedAttack} isDisabled={isLocked} onSelect={setAttack} />
          </fieldset>
          <fieldset className={styles.step}>
            <legend className={styles.stepTitle}>
              <span className={styles.stepNumber}>2</span>
              {messages.composer.durationStep}
            </legend>
            <DurationPicker
              durationsSeconds={menu.durationsSeconds}
              selected={selectedDuration}
              isDisabled={isLocked}
              onSelect={setDurationSeconds}
            />
          </fieldset>
          <div className={styles.step}>
            <h3 className={styles.stepTitle}>
              <span className={styles.stepNumber}>3</span>
              {messages.composer.defensesStep}
            </h3>
            <p className={styles.defenses}>
              <strong>{messages.composer.defensesOn}.</strong> {messages.composer.defensesDetail}
            </p>
          </div>
        </div>
        <aside className={styles.preview} aria-labelledby="preview-title">
          <h2 id="preview-title" className={styles.previewTitle}>
            {messages.preview.title}
          </h2>
          <RoundPreview attack={selectedAttack} durationSeconds={selectedDuration} />
          <div className={styles.startRow}>
            {isBusy ? <p className={styles.busy}>{messages.composer.busy}</p> : null}
            <Button type="submit" variant="primary" size="big" disabled={isLocked}>
              {isStarting ? messages.composer.starting : messages.composer.start}
            </Button>
            <p className={styles.hint}>{messages.composer.startHint}</p>
          </div>
        </aside>
      </form>
    </Sheet>
  );
}

function firstAttackOf(menu: AttackMenu): AttackId | null {
  return menu.attacks[0]?.id ?? null;
}

function preferredDurationOf(menu: AttackMenu): number {
  const hasPreferred = menu.durationsSeconds.includes(PREFERRED_DURATION_SECONDS);

  if (hasPreferred) {
    return PREFERRED_DURATION_SECONDS;
  }

  return menu.durationsSeconds[0] ?? PREFERRED_DURATION_SECONDS;
}
