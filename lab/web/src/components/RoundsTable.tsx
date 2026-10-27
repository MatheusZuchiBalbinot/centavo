import { Link } from 'react-router';

import type { RoundId } from '../api/ids';
import type { RoundSummary } from '../api/rounds';
import { totalAttacks } from '../domain/attacks';
import { formatDuration } from '../domain/duration';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';
import { roundPath } from '../routes';
import VerdictBadge from './VerdictBadge';

// Picking is offered on the rounds page, for comparing two rounds.
export type RoundPicking = {
	readonly picked: readonly RoundId[];
	readonly limit: number;
	readonly onToggle: (round: RoundId) => void;
};

type RoundsTableProps = { readonly rounds: readonly RoundSummary[]; readonly picking?: RoundPicking };

// RoundsTable lists finished rounds the way the pyle lists routes: one row
// each, numbers in mono, the verdict as a badge.
export default function RoundsTable({ rounds, picking }: RoundsTableProps): React.JSX.Element {
	return (
		// The table can be wider than the screen; the region takes focus so the
		// keyboard can scroll it too, as WCAG asks of a scrollable region.
		// eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- a scrollable region must be focusable
		<div className="table-wrap" role="region" aria-label={messages.rounds.caption} tabIndex={0}>
			<table className="tbl">
				<caption className="visually-hidden">{messages.rounds.caption}</caption>
				<thead>
					<tr>
						{picking ? <th scope="col">{messages.rounds.pick}</th> : null}
						<th scope="col">{messages.rounds.round}</th>
						<th scope="col">{messages.rounds.scenario}</th>
						<th scope="col">{messages.rounds.duration}</th>
						<th scope="col">{messages.rounds.operations}</th>
						<th scope="col">{messages.rounds.attacks}</th>
						<th scope="col">{messages.rounds.verdict}</th>
					</tr>
				</thead>
				<tbody>
					{rounds.map((round) => (
						<RoundRow key={round.round} round={round} picking={picking} />
					))}
				</tbody>
			</table>
		</div>
	);
}

function RoundRow({ round, picking }: { round: RoundSummary; picking: RoundPicking | undefined }): React.JSX.Element {
	return (
		<tr>
			{picking ? (
				<td>
					<PickBox round={round.round} picking={picking} />
				</td>
			) : null}
			<th scope="row" className="num">
				<Link to={roundPath(round.round)}>{round.round}</Link>
			</th>
			<td className="num wrap">{round.scenario}</td>
			<td className="num">{formatDuration(round.seconds)}</td>
			<td className="num">{round.operations.toLocaleString('pt-BR')}</td>
			<td className="num">{totalAttacks(round.counters).toLocaleString('pt-BR')}</td>
			<td>
				<VerdictBadge verdict={round.verdict} />
			</td>
		</tr>
	);
}

function PickBox({ round, picking }: { round: RoundId; picking: RoundPicking }): React.JSX.Element {
	const isPicked = picking.picked.includes(round);
	const isFull = picking.picked.length >= picking.limit && !isPicked;

	return (
		<input
			type="checkbox"
			checked={isPicked}
			disabled={isFull}
			aria-label={format(messages.rounds.pickLabel, { round })}
			onChange={() => picking.onToggle(round)}
		/>
	);
}
