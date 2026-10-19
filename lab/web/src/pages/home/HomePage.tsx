import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { messages } from '../../i18n/pt-BR';
import styles from './HomePage.module.css';

export default function HomePage(): React.JSX.Element {
  useDocumentTitle(messages.titles.home);

  return (
    <div className={styles.hero}>
      <h1 id={PAGE_HEADING_ID} className={styles.headline} tabIndex={-1}>
        {messages.home.headline}
      </h1>
      <p className={styles.lead}>{messages.home.lead}</p>
    </div>
  );
}
