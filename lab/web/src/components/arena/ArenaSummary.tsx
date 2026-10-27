import type { AuditorReading, GuaranteeCheck, Replay } from '../../api/replay';
import { formatElapsed } from '../../domain/duration';
import { GUARANTEE_IDS, guaranteeCopy, guaranteeView, type GuaranteeId, type GuaranteeView } from '../../domain/guarantees';
import { meanRecoveryMs, type Incident } from '../../domain/incidents';
import { formatCents } from '../../domain/money';
import type { Narration } from '../../domain/narrator';
import { isAttackEvent, isDefenseEvent } from '../../domain/phase';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';
import IncidentLog from './IncidentLog';

const MS_PER_SECOND = 1_000;

type ArenaSummaryProps = {
	readonly replay: Replay;
	readonly reading: AuditorReading | null;
	readonly seen: Replay['events'];
	readonly incidents: readonly Incident[];
	readonly headline: Narration;
	readonly isFinished: boolean;
	readonly isLive: boolean;
	readonly wallMs: number;
};

export default function ArenaSummary(props: ArenaSummaryProps): React.JSX.Element {
	const { replay, reading, seen, incidents, headline } = props;

	return (
		<>
			<MoneyCard {...props} />
			<IncidentLog incidents={incidents} headline={headline} />
			<Stats replay={replay} seen={seen} incidents={incidents} />
			<GuaranteeChips replay={replay} reading={reading} />
		</>
	);
}

function MoneyCard(props: ArenaSummaryProps): React.JSX.Element {
	const view = moneyViewOf(props);

	return (
		<section className={styles.card} aria-labelledby="arena-money">
			<h3 id="arena-money" className={styles.cardTitle}>
				{messages.arena.money}
			</h3>
			<p className={styles.money} data-broken={view.isBroken}>
				{view.value}
			</p>
			<p className={styles.hint}>{view.hint}</p>
		</section>
	);
}

type MoneyView = { readonly value: string; readonly hint: string; readonly isBroken: boolean };

function moneyViewOf({ replay, reading, isFinished, isLive, wallMs }: ArenaSummaryProps): MoneyView {
	if (isFinished) {
		const isBroken = replay.checks.some((check) => check.id === 'I1' && check.status === 'failed');
		const value = isBroken ? messages.round.moneyBrokenValue : formatCents(0);

		return { value, hint: messages.arena.moneyHintDone, isBroken };
	}

	if (!isLive) {
		return { value: messages.arena.moneyUnread, hint: messages.arena.moneyHintReplay, isBroken: false };
	}

	const isUnread = reading === null || reading.isUnavailable;

	if (isUnread) {
		return { value: messages.arena.moneyUnread, hint: messages.round.moneyHintLive, isBroken: false };
	}

	const seconds = Math.max(0, Math.round((wallMs - reading.at.getTime()) / MS_PER_SECOND));

	return {
		value: formatCents(reading.sumCents),
		hint: format(messages.arena.moneyHint, { seconds }),
		isBroken: reading.sumCents !== 0,
	};
}

type StatsProps = { readonly replay: Replay; readonly seen: Replay['events']; readonly incidents: readonly Incident[] };

function Stats({ replay, seen, incidents }: StatsProps): React.JSX.Element {
	const operations = replay.seconds.reduce((total, second) => total + second.ok + second.fail + second.info, 0);
	const unknown = replay.seconds.reduce((total, second) => total + second.info, 0);
	const recoveryMs = meanRecoveryMs(incidents);
	const stats = [
		{ key: 'operations', value: countText(operations), label: messages.arena.stats.operations },
		{ key: 'attacks', value: countText(seen.filter(isAttackEvent).length), label: messages.arena.stats.attacks },
		{ key: 'defenses', value: countText(seen.filter(isDefenseEvent).length), label: messages.arena.stats.defenses },
		{ key: 'unknown', value: countText(unknown), label: messages.arena.stats.unknown },
		{ key: 'recovery', value: recoveryMs === null ? messages.arena.statsNone : formatElapsed(recoveryMs), label: messages.arena.stats.recovery },
	];

	return (
		<section className={`${styles.card ?? ''} ${styles.stats ?? ''}`} aria-label={messages.round.board}>
			{stats.map((stat) => (
				<p key={stat.key} className={styles.stat} data-kind={stat.key}>
					<b>{stat.value}</b>
					<span>{stat.label}</span>
				</p>
			))}
		</section>
	);
}

function countText(count: number): string {
	return count.toLocaleString('pt-BR');
}

function GuaranteeChips({ replay, reading }: { replay: Replay; reading: AuditorReading | null }): React.JSX.Element {
	return (
		<section className={styles.card} aria-labelledby="arena-guarantees">
			<h3 id="arena-guarantees" className={styles.cardTitle}>
				{messages.arena.guarantees}
			</h3>
			<ul className={styles.chips}>
				{GUARANTEE_IDS.map((id) => (
					<GuaranteeChip key={id} id={id} view={guaranteeView(id, replay.checks, reading)} />
				))}
			</ul>
			<p className={styles.hint}>{messages.arena.guaranteesHint}</p>
		</section>
	);
}

function GuaranteeChip({ id, view }: { id: GuaranteeId; view: GuaranteeView }): React.JSX.Element {
	const copy = guaranteeCopy(id);

	// A button so the keyboard reaches the full name in the tooltip, as the
	// glossary's terms do.
	return (
		<li>
			<button type="button" className={styles.guaranteeChip} data-tone={toneOf(view)} data-tooltip={copy.name}>
				{copy.short}
			</button>
		</li>
	);
}

function toneOf(view: GuaranteeView): 'ok' | 'broken' | 'later' {
	switch (view.kind) {
		case 'later':
			return 'later';
		case 'live':
			return view.isHolding ? 'ok' : 'broken';
		case 'final':
			return finalTone(view.status);
	}
}

function finalTone(status: GuaranteeCheck['status']): 'ok' | 'broken' | 'later' {
	if (status === 'not_checked') {
		return 'later';
	}

	return status === 'failed' ? 'broken' : 'ok';
}
