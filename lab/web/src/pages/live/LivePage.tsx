import { Link, Navigate, useLocation, useNavigate } from 'react-router';

import ReplayArena from '../../components/arena/ReplayArena';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useLabReading } from '../../hooks/useLabReading';
import { useNow } from '../../hooks/useNow';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { useViewMode } from '../../hooks/viewModeContext';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { QUERY, roundPath, ROUTES } from '../../routes';
import { useShowcaseReplay } from './useShowcaseReplay';

// The showcase plays at this speed and rests on the verdict this long.
const SHOWCASE_SPEED = 30;
const SHOWCASE_HOLD_MS = 8_000;
const CLOCK_TICK_MS = 1_000;

// LivePage leads to the round the lab is running, as soon as it has an id.
export default function LivePage(): React.JSX.Element {
	useDocumentTitle(messages.live.title);

	const lab = useLabReading();
	// The query goes along: a surprise round opens in the arena, with its bet.
	const { search } = useLocation();
	const isPresenting = useViewMode().mode === 'presentation';
	const isIdle = lab.kind === 'ready' && lab.state.status === 'idle';
	const isShowcase = isPresenting && isIdle;
	const navigate = useNavigate();
	const showcase = useShowcaseReplay(isShowcase);
	const now = useNow(CLOCK_TICK_MS, isShowcase);
	const round = lab.kind === 'ready' && lab.state.status !== 'idle' ? lab.state.round : null;

	// Leaving the showcase opens the round it was replaying, without the
	// arena that Presentation mode would open again.
	function handleCloseShowcase(): void {
		if (showcase.kind !== 'ready') {
			return;
		}

		const closedArena = new URLSearchParams({ [QUERY.arena]: '0' }).toString();

		void navigate({ pathname: roundPath(showcase.replay.round), search: closedArena });
	}

	if (round !== null) {
		return <Navigate to={{ pathname: roundPath(round), search }} replace />;
	}

	const isPreparing = lab.kind === 'ready' && lab.state.status !== 'idle';

	return (
		<>
			{isShowcase && showcase.kind === 'ready' ? (
				// Presentation mode never starts a round: with the lab free it
				// replays the newest one, over and over.
				<ReplayArena
					eyebrow={format(messages.arena.eyebrowShowcase, { round: showcase.replay.round })}
					replay={showcase.replay}
					startMs={0}
					speed={SHOWCASE_SPEED}
					isLooping
					holdMs={SHOWCASE_HOLD_MS}
					wallMs={now}
					onClose={handleCloseShowcase}
				/>
			) : null}
			<h1 id={PAGE_HEADING_ID} className="page-title" tabIndex={-1}>
				{isPreparing ? messages.live.preparing : messages.live.headline}
			</h1>
			{isPreparing ? null : (
				<p className="page-desc">
					{messages.live.detail} <Link to={ROUTES.home}>{messages.live.back}</Link>
				</p>
			)}
		</>
	);
}
