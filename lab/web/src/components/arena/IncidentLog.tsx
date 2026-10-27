import { Clock, ShieldCheck } from 'lucide-react';

import { formatElapsed } from '../../domain/duration';
import type { Incident, Recovery, Response } from '../../domain/incidents';
import type { Narration } from '../../domain/narrator';
import { format } from '../../i18n/format';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';

// The rest stays on the timeline; the side column keeps the latest.
const MAX_SHOWN = 5;
const ICON_SIZE = 14;

type IncidentLogProps = { readonly incidents: readonly Incident[]; readonly headline: Narration };

export default function IncidentLog({ incidents, headline }: IncidentLogProps): React.JSX.Element {
	const shown = incidents.slice(0, MAX_SHOWN);
	const older = incidents.length - shown.length;

	return (
		<section className={styles.card} aria-labelledby="arena-incidents">
			<h3 id="arena-incidents" className={styles.cardTitle}>
				{messages.arena.incidents.title}
			</h3>
			<p className={headline.isAttack ? 'visually-hidden' : styles.phaseLine} aria-live="polite">
				{headline.text}
			</p>
			{shown.length === 0 ? (
				<p className={styles.hint}>{messages.arena.incidents.empty}</p>
			) : (
				<ol className={styles.incidents}>
					{shown.map((incident) => (
						<IncidentItem key={incident.key} incident={incident} />
					))}
				</ol>
			)}
			{older > 0 ? <p className={styles.hint}>{format(messages.arena.incidents.older, { count: older })}</p> : null}
		</section>
	);
}

function IncidentItem({ incident }: { incident: Incident }): React.JSX.Element {
	return (
		<li className={styles.incident} data-manual={incident.isManual}>
			<p className={styles.incidentHead}>
				<span className={styles.incidentLabel}>
					<span className="visually-hidden">{messages.arena.incidents.attack} </span>
					{incident.label}
				</span>
				<span>{incident.what}</span>
				{incident.isManual ? <span className={styles.youBadge}>{messages.arena.incidents.you}</span> : null}
			</p>
			<ResponseLine response={incident.response} />
			<p className={styles.incidentLine} data-recovery={incident.recovery.kind}>
				<Clock size={ICON_SIZE} aria-hidden="true" />
				{recoveryText(incident.recovery)}
			</p>
		</li>
	);
}

function ResponseLine({ response }: { response: Response }): React.JSX.Element {
	if (response.kind !== 'answered') {
		return (
			<p className={styles.incidentLine} data-response={response.kind}>
				<ShieldCheck size={ICON_SIZE} aria-hidden="true" />
				{response.kind === 'waiting' ? messages.arena.incidents.waiting : messages.arena.incidents.noResponse}
			</p>
		);
	}

	return (
		<p className={styles.incidentLine} data-response="answered">
			<ShieldCheck size={ICON_SIZE} aria-hidden="true" />
			<span>
				{response.what} <b>{format(messages.arena.incidents.after, { elapsed: formatElapsed(response.afterMs) })}</b>
			</span>
		</p>
	);
}

function recoveryText(recovery: Recovery): string {
	const copy = messages.arena.incidents.recovery;

	switch (recovery.kind) {
		case 'back':
			return format(copy.back, { elapsed: formatElapsed(recovery.afterMs) });
		case 'out':
			return copy.out;
		case 'until_cure':
			return copy.until_cure;
		case 'absorbed':
			return copy.absorbed;
	}
}
