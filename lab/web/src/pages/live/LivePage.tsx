import { Link, Navigate } from 'react-router';

import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useLabReading } from '../../hooks/useLabReading';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { messages } from '../../i18n/pt-BR';
import { roundPath, ROUTES } from '../../routes';

// LivePage leads to the round the lab is running, as soon as it has an id.
export default function LivePage(): React.JSX.Element {
  useDocumentTitle(messages.live.title);

  const lab = useLabReading();
  const round = lab.kind === 'ready' && lab.state.status !== 'idle' ? lab.state.round : null;

  if (round !== null) {
    return <Navigate to={roundPath(round)} replace />;
  }

  const isPreparing = lab.kind === 'ready' && lab.state.status !== 'idle';

  return (
    <>
      <h1 id={PAGE_HEADING_ID} tabIndex={-1}>
        {isPreparing ? messages.live.preparing : messages.live.headline}
      </h1>
      {isPreparing ? null : (
        <p>
          {messages.live.detail} <Link to={ROUTES.home}>{messages.live.back}</Link>
        </p>
      )}
    </>
  );
}
