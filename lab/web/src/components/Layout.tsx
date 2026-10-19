import { NavLink, Outlet } from 'react-router';

import { useLabState } from '../hooks/useLabState';
import { useRouteFocus } from '../hooks/useRouteFocus';
import { messages } from '../i18n/pt-BR';
import { ROUTES } from '../routes';
import LabStatusPill from './LabStatusPill';
import { ToastProvider } from './ui/ToastProvider';
import styles from './Layout.module.css';

const MAIN_ID = 'main';

export default function Layout(): React.JSX.Element {
  const reading = useLabState();
  const isRunning = reading.kind === 'ready' && reading.state.status !== 'idle';

  useRouteFocus();

  return (
    <ToastProvider>
      <a className={styles.skipLink} href={`#${MAIN_ID}`}>
        {messages.skipToContent}
      </a>
      <header className={styles.bar}>
        <span className={styles.brand}>
          <span className={styles.coin} aria-hidden="true">
            ¢
          </span>
          {messages.appName}
        </span>
        <nav aria-label={messages.nav.label}>
          <ul className={styles.nav}>
            <li>
              <NavLink to={ROUTES.home} end>
                {messages.nav.home}
              </NavLink>
            </li>
            {isRunning ? (
              <li>
                <NavLink to={ROUTES.live}>{messages.nav.live}</NavLink>
              </li>
            ) : null}
          </ul>
        </nav>
        <p className={styles.notice}>{messages.labNotice}</p>
        <LabStatusPill reading={reading} />
      </header>
      <main id={MAIN_ID} className={styles.main} tabIndex={-1}>
        <Outlet context={reading} />
      </main>
    </ToastProvider>
  );
}
