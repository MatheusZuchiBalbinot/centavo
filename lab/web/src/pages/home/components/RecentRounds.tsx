import type { RoundSummary } from '../../../api/rounds';
import RoundsTable from '../../../components/RoundsTable';
import { Card } from '../../../components/ui/Card';
import { messages } from '../../../i18n/pt-BR';

// How many rounds the home page lists.
const ROUNDS_SHOWN = 6;

type RecentRoundsProps = { readonly rounds: readonly RoundSummary[] };

export default function RecentRounds({ rounds }: RecentRoundsProps): React.JSX.Element {
	const shown = rounds.slice(0, ROUNDS_SHOWN);

	return (
		<Card title={messages.recent.title} titleId="recent-title" bodyClassName="" aside={<span className="card-note">{messages.recent.note}</span>}>
			{shown.length === 0 ? <p className="card-body muted">{messages.recent.empty}</p> : <RoundsTable rounds={shown} />}
		</Card>
	);
}
