import { FlaskConical } from 'lucide-react';
import { Link } from 'react-router';

import type { RoundSummary, Verdict } from '../../../api/rounds';
import { betScore, isBetBook } from '../../../domain/bets';
import { scoreboardOf, type RoundRecord } from '../../../domain/scoreboard';
import { format } from '../../../i18n/format';
import { messages } from '../../../i18n/pt-BR';
import { roundPath } from '../../../routes';
import { readStored, STORAGE_KEYS } from '../../../storage';
import styles from './LabNow.module.css';

type LabNowProps = { readonly rounds: readonly RoundSummary[] };

// LabNow sums up the rounds the page read, and says over how many.
export default function LabNow({ rounds }: LabNowProps): React.JSX.Element {
	const operations = rounds.reduce(addOperations, 0);
	const broken = rounds.filter(isBroken).length;
	const board = scoreboardOf(rounds);

	return (
		<aside className={styles.labNow} aria-labelledby="lab-now-title">
			<div className={styles.head}>
				<h2 id="lab-now-title" className={styles.eyebrow}>
					{messages.labNow.title}
				</h2>
				<span className={styles.icon} aria-hidden="true">
					<FlaskConical size={16} />
				</span>
			</div>
			<p className={styles.streak} data-empty={board.streak === 0}>
				{streakText(board.streak)}
			</p>
			<p className={styles.figure}>{operations.toLocaleString('pt-BR')}</p>
			<p className={styles.figureLabel}>{messages.labNow.operations}</p>
			<dl className={styles.figures}>
				<div>
					<dt>{messages.labNow.rounds}</dt>
					<dd>{rounds.length.toLocaleString('pt-BR')}</dd>
				</div>
				<div>
					<dt>{messages.labNow.broken}</dt>
					<dd data-tone={broken > 0 ? 'broken' : 'ok'}>{broken.toLocaleString('pt-BR')}</dd>
				</div>
			</dl>
			<Records mostAttacks={board.mostAttacks} mostKills={board.mostKills} />
			<BetScore rounds={rounds} />
			<p className={styles.note}>{rounds.length === 0 ? messages.labNow.empty : format(messages.labNow.window, { count: rounds.length })}</p>
		</aside>
	);
}

function addOperations(total: number, round: RoundSummary): number {
	return total + round.operations;
}

function isBroken(round: RoundSummary): boolean {
	return round.verdict === 'fail';
}

type RecordsProps = { readonly mostAttacks: RoundRecord | null; readonly mostKills: RoundRecord | null };

function Records({ mostAttacks, mostKills }: RecordsProps): React.JSX.Element | null {
	const entries = [
		{ key: 'attacks', label: messages.labNow.mostAttacks, record: mostAttacks },
		{ key: 'kills', label: messages.labNow.mostKills, record: mostKills },
	];
	const records = entries.flatMap(({ key, label, record }) => (record === null ? [] : [{ key, label, record }]));

	if (records.length === 0) {
		return null;
	}

	return (
		<section className={styles.records} aria-labelledby="lab-records">
			<h3 id="lab-records" className={styles.recordsTitle}>
				{messages.labNow.records}
			</h3>
			<dl>
				{records.map((entry) => (
					<RecordRow key={entry.key} label={entry.label} record={entry.record} />
				))}
			</dl>
		</section>
	);
}

function RecordRow({ label, record }: { label: string; record: RoundRecord }): React.JSX.Element {
	return (
		<div className={styles.record}>
			<dt>{label}</dt>
			<dd>
				<Link to={roundPath(record.round)}>{format(messages.labNow.recordValue, { count: record.count, round: record.round })}</Link>
			</dd>
		</div>
	);
}

// BetScore is the person's own score: their bets live in their browser.
function BetScore({ rounds }: { rounds: readonly RoundSummary[] }): React.JSX.Element | null {
	const book = readStored(STORAGE_KEYS.bets, isBetBook) ?? {};
	const verdicts = new Map<string, Verdict | null>(rounds.map((round) => [round.round, round.verdict]));
	const score = betScore(book, verdicts);
	const hasBets = score.right + score.wrong > 0;

	if (!hasBets) {
		return null;
	}

	return <p className={styles.bets}>{format(messages.labNow.bets, score)}</p>;
}

function streakText(streak: number): string {
	if (streak === 0) {
		return messages.labNow.streakNone;
	}

	return streak === 1 ? messages.labNow.streakOne : format(messages.labNow.streakMany, { count: streak });
}
