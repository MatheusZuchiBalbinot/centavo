import { useState } from 'react';
import { useParams } from 'react-router';

import { isRoundId, type RoundId } from '../../api/ids';
import type { Replay } from '../../api/replay';
import { Button, LinkButton } from '../../components/ui/Button';
import { headlineOf } from '../../domain/headline';
import type { AttackRange } from '../../domain/highlight';
import { phaseOf } from '../../domain/phase';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useNow } from '../../hooks/useNow';
import { PAGE_HEADING_ID } from '../../hooks/useRouteFocus';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import { ROUTES } from '../../routes';
import NotFoundPage from '../not-found/NotFoundPage';
import EventFeed from './components/EventFeed';
import Guarantees from './components/Guarantees';
import ResultAside from './components/ResultAside';
import RoundArena from './components/RoundArena';
import RoundChart from './components/RoundChart';
import RoundHero from './components/RoundHero';
import styles from './components/RoundPage.module.css';
import RoundTiles from './components/RoundTiles';
import { useArenaQuery } from './hooks/useArenaQuery';
import { useAttackToasts } from './hooks/useAttackToasts';
import { useRoundStream, type StreamAvailability } from './hooks/useRoundStream';

const CLOCK_TICK_MS = 1_000;
const MS_PER_SECOND = 1_000;
const CALM_SCENARIOS: ReadonlySet<string> = new Set(['smoke', 'concurrency-hotspot']);

export default function RoundPage(): React.JSX.Element {
	const { round } = useParams();

	if (!isRoundId(round)) {
		return <NotFoundPage />;
	}

	return <FollowedRound round={round} />;
}

function FollowedRound({ round }: { round: RoundId }): React.JSX.Element {
	const { state, isConnected, lastMessageAt, availability, retry } = useRoundStream(round);
	const [highlight, setHighlight] = useState<AttackRange | null>(null);
	const isFinished = state.kind === 'finished';
	const now = useNow(CLOCK_TICK_MS, !isFinished);
	const events = state.kind === 'connecting' ? [] : state.replay.events;
	const newEvents = state.kind === 'connecting' ? 0 : state.newEvents;
	const arena = useArenaQuery();

	useAttackToasts(events, newEvents);
	useDocumentTitle(titleOf(round, isFinished, availability));

	if (state.kind === 'connecting') {
		return <Unopened round={round} availability={availability} onRetry={retry} />;
	}

	const { replay, reading } = state;
	const phase = phaseOf(replay, isFinished);
	const elapsedSeconds = elapsedOf(replay, now);
	const nowSeconds = phase === 'attack' ? elapsedSeconds : null;
	const headline = headlineOf({ replay, phase, elapsedSeconds, reading });
	const ageSeconds = lastMessageAt === null ? null : Math.max(0, Math.round((now - lastMessageAt) / MS_PER_SECOND));
	const isCalm = CALM_SCENARIOS.has(replay.scenario);

	return (
		<article className={styles.round}>
			<RoundHero
				round={round}
				replay={replay}
				phase={phase}
				headline={headline}
				elapsedSeconds={elapsedSeconds}
				isConnected={isConnected}
				onOpenArena={arena.open}
			/>
			<RoundTiles replay={replay} reading={reading} isFinished={isFinished} />
			<div className={styles.grid}>
				<div className={styles.main}>
					<RoundChart
						replay={replay}
						isLive={!isFinished}
						nowSeconds={nowSeconds}
						ageSeconds={ageSeconds}
						highlight={highlight}
						onHighlight={setHighlight}
					/>
					<Guarantees checks={replay.checks} reading={reading} isFinished={isFinished} />
				</div>
				{isFinished ? (
					<ResultAside replay={replay} />
				) : (
					<EventFeed events={replay.events} isCalm={isCalm} highlight={highlight} onHighlight={setHighlight} />
				)}
			</div>
			{isFinished ? <EventFeed events={replay.events} isCalm={isCalm} highlight={highlight} onHighlight={setHighlight} /> : null}
			<RoundArena round={round} replay={replay} reading={reading} phase={phase} headline={headline} now={now} ageSeconds={ageSeconds} query={arena} />
		</article>
	);
}

// elapsedOf is how long the attack has been running, from the round's own
// start; it stops growing at the planned duration.
function elapsedOf(replay: Replay, now: number): number {
	if (replay.startedAt === null) {
		return replay.seconds.length;
	}

	const elapsed = (now - replay.startedAt.getTime()) / MS_PER_SECOND;

	return Math.max(0, Math.min(elapsed, replay.durationSeconds || elapsed));
}

type UnopenedProps = {
	readonly round: RoundId;
	readonly availability: StreamAvailability;
	readonly onRetry: () => void;
};

// Unopened is the page before the first snapshot: still connecting, a round
// that does not exist, or a lab that did not answer.
function Unopened({ round, availability, onRetry }: UnopenedProps): React.JSX.Element {
	switch (availability) {
		case 'open':
			return (
				<h1 id={PAGE_HEADING_ID} className={styles.connecting} tabIndex={-1}>
					{messages.round.connecting}
				</h1>
			);
		case 'missing':
			return (
				<div className={styles.unopened}>
					<h1 id={PAGE_HEADING_ID} className="page-title" tabIndex={-1}>
						{format(messages.round.missing, { round })}
					</h1>
					<p className="page-desc">{messages.round.missingDetail}</p>
					<LinkButton to={ROUTES.rounds} variant="primary">
						{messages.round.toList}
					</LinkButton>
				</div>
			);
		case 'unreachable':
			return (
				<div className={styles.unopened} role="alert">
					<h1 id={PAGE_HEADING_ID} className="page-title" tabIndex={-1}>
						{messages.round.unreachable}
					</h1>
					<p className="page-desc">{format(messages.round.unreachableDetail, { round })}</p>
					<Button variant="primary" onClick={onRetry}>
						{messages.round.retry}
					</Button>
				</div>
			);
	}
}

function titleOf(round: RoundId, isFinished: boolean, availability: StreamAvailability): string {
	if (availability === 'missing') {
		return messages.titles.notFound;
	}

	return format(isFinished ? messages.round.titleDone : messages.round.titleLive, { round });
}
