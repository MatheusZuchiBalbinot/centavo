import { Link } from 'react-router';

import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { messages } from '../../i18n/pt-BR';
import { ROUTES } from '../../routes';

export default function NotFoundPage(): React.JSX.Element {
	useDocumentTitle(messages.titles.notFound);

	return (
		<>
			<h1 id={PAGE_HEADING_ID} className="page-title" tabIndex={-1}>
				{messages.notFound.headline}
			</h1>
			<p>
				<Link to={ROUTES.home}>{messages.notFound.back}</Link>
			</p>
		</>
	);
}
