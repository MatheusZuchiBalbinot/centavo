import { useNavigate } from 'react-router';

import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useLabReading } from '../../hooks/useLabReading';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { messages } from '../../i18n/pt-BR';
import { ROUTES } from '../../routes';
import Composer from './components/Composer';
import LabNow from './components/LabNow';
import RecentRounds from './components/RecentRounds';
import styles from './HomePage.module.css';
import type { StartChoice } from './hooks/useStartRound';
import { useAttackMenu } from './hooks/useAttackMenu';
import { useRecentRounds } from './hooks/useRecentRounds';
import { useStartRound } from './hooks/useStartRound';

export default function HomePage(): React.JSX.Element {
  useDocumentTitle(messages.titles.home);

  const lab = useLabReading();
  const menu = useAttackMenu();
  const labStatus = lab.kind === 'ready' ? lab.state.status : lab.kind;
  const recent = useRecentRounds(labStatus);
  const { isStarting, start } = useStartRound();
  const navigate = useNavigate();

  const isBusy = labStatus === 'running' || labStatus === 'stopping';
  const rounds = recent.kind === 'ready' ? recent.rounds : [];

  async function startAndFollow(choice: StartChoice): Promise<void> {
    const isStarted = await start(choice);

    if (isStarted) {
      await navigate(ROUTES.live);
    }
  }

  function handleStart(choice: StartChoice): void {
    void startAndFollow(choice);
  }

  return (
    <>
      <div className={styles.hero}>
        <div className={styles.intro}>
          <h1 id={PAGE_HEADING_ID} className={styles.headline} tabIndex={-1}>
            {messages.home.headline}
          </h1>
          <p className={styles.lead}>{messages.home.lead}</p>
        </div>
        <LabNow rounds={rounds} />
      </div>
      <MenuSection menu={menu} isBusy={isBusy} isStarting={isStarting} onStart={handleStart} />
      <RecentRounds rounds={rounds} />
    </>
  );
}

type MenuSectionProps = {
  readonly menu: ReturnType<typeof useAttackMenu>;
  readonly isBusy: boolean;
  readonly isStarting: boolean;
  readonly onStart: (choice: StartChoice) => void;
};

function MenuSection({ menu, isBusy, isStarting, onStart }: MenuSectionProps): React.JSX.Element {
  switch (menu.kind) {
    case 'loading':
      return <p className={styles.note}>{messages.composer.loading}</p>;
    case 'failed':
      return (
        <p className={styles.note} role="alert">
          {messages.composer.menuFailed}
        </p>
      );
    case 'ready':
      return <Composer menu={menu.menu} isBusy={isBusy} isStarting={isStarting} onStart={onStart} />;
  }
}
