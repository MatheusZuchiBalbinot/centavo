import type { Replay } from '../../../api/replay';
import { reportPath } from '../../../api/routes';
import { Badge } from '../../../components/ui/Badge';
import { LinkButton } from '../../../components/ui/Button';
import { Card } from '../../../components/ui/Card';
import { brokenCount } from '../../../domain/guarantees';
import { disabledDefenses } from '../../../domain/headline';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import { comparePath, ROUTES } from '../../../routes';
import { useControlRound } from '../hooks/useControlRound';
import styles from './RoundPage.module.css';

type ResultAsideProps = { readonly replay: Replay };

// ResultAside puts the verdict next to what the defense profile predicted: a
// control round that breaks is the expected outcome, not a failure.
export default function ResultAside({ replay }: ResultAsideProps): React.JSX.Element {
	const disabled = disabledDefenses(replay.defenses);
	const isControl = disabled.length > 0;
	const expected = isControl ? format(messages.round.expectedFail, { defenses: disabled.join(', ') }) : messages.round.expectedPass;
	const broken = brokenCount(replay.checks);
	const isMatch = isControl ? replay.verdict === 'fail' : replay.verdict === 'pass';
	const isRobust = replay.defenses !== '' && !isControl;
	const control = useControlRound(replay.scenario, isRobust);

	return (
		<div className={styles.side}>
			<Card title={messages.round.expectedTitle} titleId="expected-title">
				<dl className={styles.expectedList}>
					<div>
						<dt>{messages.round.expectedLabel}</dt>
						<dd>{expected}</dd>
					</div>
					<div>
						<dt>{messages.round.actualLabel}</dt>
						<dd className={styles.actual}>
							<Badge tone={replay.verdict === 'pass' ? 'success' : 'danger'}>{verdictLabel(replay)}</Badge>
							{format(messages.round.brokenCount, { count: broken })}
						</dd>
					</div>
				</dl>
				<p className={styles.match} data-match={isMatch}>
					{matchText(isMatch, isControl)}
				</p>
			</Card>
			<Card title={messages.round.nextTitle} titleId="next-title">
				<div className={styles.actions}>
					<LinkButton to={ROUTES.home} variant="primary">
						{messages.round.again}
					</LinkButton>
					{control === null ? null : (
						<LinkButton to={comparePath(replay.round, control)} data-tooltip={messages.round.withoutDefenseHelp}>
							{messages.round.withoutDefense}
						</LinkButton>
					)}
					<a className="btn" href={reportPath(replay.round)}>
						{messages.round.report}
					</a>
				</div>
			</Card>
		</div>
	);
}

function verdictLabel(replay: Replay): string {
	return replay.verdict === null ? messages.verdict.none : messages.verdict[replay.verdict];
}

function matchText(isMatch: boolean, isControl: boolean): string {
	if (!isMatch) {
		return messages.round.mismatch;
	}

	return isControl ? messages.round.matchControl : messages.round.match;
}
