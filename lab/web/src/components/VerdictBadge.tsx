import type { Verdict } from '../api/rounds';
import { messages } from '../i18n/pt-BR';
import { Badge, type BadgeTone } from './ui/Badge';

// VerdictBadge says how a round ended, in words and in color.
export default function VerdictBadge({ verdict }: { verdict: Verdict | null }): React.JSX.Element {
	return <Badge tone={toneOf(verdict)}>{verdict === null ? messages.verdict.none : messages.verdict[verdict]}</Badge>;
}

function toneOf(verdict: Verdict | null): BadgeTone {
	switch (verdict) {
		case 'pass':
			return 'success';
		case 'fail':
			return 'danger';
		case 'inconclusive':
			return 'warning';
		case null:
			return 'muted';
	}
}
