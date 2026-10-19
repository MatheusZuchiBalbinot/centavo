import { Gauge, History, Radio } from 'lucide-react';
import { NavLink, Outlet, useLocation } from 'react-router';

import { useLabState } from '../hooks/useLabState';
import { useRouteFocus } from '../hooks/useRouteFocus';
import { messages } from '../i18n/pt-BR';
import { roundPath, ROUTES } from '../routes';
import LabStatus from './LabStatus';
import styles from './Layout.module.css';
import { ToastProvider } from './ui/ToastProvider';

const MAIN_ID = 'main';
const ICON_SIZE = 16;

export default function Layout(): React.JSX.Element {
  const reading = useLabState();
  const isRunning = reading.kind === 'ready' && reading.state.status !== 'idle';
  const { pathname } = useLocation();
  const liveRound = reading.kind === 'ready' && reading.state.status !== 'idle' ? reading.state.round : null;
  const isOnLiveRound = liveRound !== null && pathname === roundPath(liveRound);

  // The round the lab is running belongs to "Rodada ao vivo", not to the list.
  function liveNavClass(): string {
    return navClass({ isActive: isOnLiveRound || pathname === ROUTES.live });
  }

  function roundsNavClass({ isActive }: { isActive: boolean }): string {
    return navClass({ isActive: isActive && !isOnLiveRound });
  }

  useRouteFocus();

  return (
    <ToastProvider>
      <a className={styles.skipLink} href={`#${MAIN_ID}`}>
        {messages.skipToContent}
      </a>
      <div className={styles.shell}>
        <aside className={styles.sidebar}>
          <div className={styles.brand}>
            <span className={styles.mark} aria-hidden="true">
              ¢
            </span>
            <span className={styles.brandText}>
              <span className={styles.brandName}>{messages.appName}</span>
              <span className={styles.brandSub}>{messages.brandSub}</span>
            </span>
          </div>
          <nav className={styles.nav} aria-label={messages.nav.label}>
            <p className={styles.navGroup}>{messages.nav.group}</p>
            <ul>
              <li>
                <NavLink to={ROUTES.home} end className={navClass}>
                  <Gauge size={ICON_SIZE} aria-hidden="true" />
                  {messages.nav.home}
                </NavLink>
              </li>
              <li>
                <NavLink to={ROUTES.live} className={liveNavClass}>
                  <Radio size={ICON_SIZE} aria-hidden="true" />
                  {messages.nav.live}
                  {isRunning ? <span className={styles.navLive}>{messages.nav.liveBadge}</span> : null}
                </NavLink>
              </li>
              <li>
                <NavLink to={ROUTES.rounds} className={roundsNavClass}>
                  <History size={ICON_SIZE} aria-hidden="true" />
                  {messages.nav.rounds}
                </NavLink>
              </li>
            </ul>
          </nav>
          <p className={styles.notice}>{messages.labNotice}</p>
        </aside>
        <div className={styles.main}>
          <header className={styles.topbar}>
            <span className={styles.topbarTitle}>{messages.topbar}</span>
            <LabStatus reading={reading} />
          </header>
          <main id={MAIN_ID} className="page" tabIndex={-1}>
            <Outlet context={reading} />
          </main>
        </div>
      </div>
    </ToastProvider>
  );
}

function navClass({ isActive }: { isActive: boolean }): string {
  return isActive ? `${styles.navItem} ${styles.navItemOn}` : (styles.navItem ?? '');
}
