import { Link, useNavigate } from 'react-router';

import { LinkButton } from '../../components/ui/Button';
import { StatusIntro } from '../../components/ui/StatusIntro';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useLabReading } from '../../hooks/useLabReading';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { messages } from '../../i18n/pt-BR';
import { ROUTES } from '../../routes';
import Composer from './components/Composer';
import LabNow from './components/LabNow';
import RecentRounds from './components/RecentRounds';
import styles from './HomePage.module.css';
import { useAttackMenu } from './hooks/useAttackMenu';
import { useRecentRounds } from './hooks/useRecentRounds';
import { useStartRound } from './hooks/useStartRound';
import type { StartChoice } from './hooks/useStartRound';

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
    <div className={styles.page}>
      <div className={styles.hero}>
        <StatusIntro
          tone={isBusy ? 'info' : 'healthy'}
          isLive={isBusy}
          eyebrow={isBusy ? messages.home.eyebrowBusy : messages.home.eyebrowFree}
          title={messages.home.headline}
          titleId={PAGE_HEADING_ID}
          headingLevel={1}
          description={<p>{messages.home.lead}</p>}
          footer={
            isBusy ? (
              <LinkButton to={ROUTES.live} variant="primary">
                {messages.home.watch}
              </LinkButton>
            ) : (
              <span className={styles.heroHint}>{messages.home.hint}</span>
            )
          }
        />
        <LabNow rounds={rounds} />
      </div>
      <MenuSection menu={menu} isBusy={isBusy} isStarting={isStarting} onStart={handleStart} />
      <RecentRounds rounds={rounds} />
      <p className={styles.more}>
        <Link to={ROUTES.rounds}>{messages.home.allRounds}</Link>
      </p>
    </div>
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
      return <p className="muted">{messages.composer.loading}</p>;
    case 'failed':
      return <p role="alert">{messages.composer.menuFailed}</p>;
    case 'ready':
      return <Composer menu={menu.menu} isBusy={isBusy} isStarting={isStarting} onStart={onStart} />;
  }
}
