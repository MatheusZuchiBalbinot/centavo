import { isManualOccurrence, type Occurrence, type OccurrenceKind } from '../../domain/occurrences';
import { messages } from '../../i18n/pt-BR';
import styles from './Arena.module.css';

// The order the legend names the kinds in: the chaos, the bank, the money.
const KIND_ORDER: readonly OccurrenceKind[] = [
	'kill',
	'freeze',
	'network',
	'failpoint',
	'database',
	'clock',
	'partner',
	'other',
	'defense',
	'transfer',
	'pix',
];

type OccurrenceLegendProps = { readonly occurrences: readonly Occurrence[]; readonly hasCure: boolean };

// Only what the round has, so the legend stays short.
export default function OccurrenceLegend({ occurrences, hasCure }: OccurrenceLegendProps): React.JSX.Element {
	const copy = messages.arena.occurrences;
	const kinds = new Set(occurrences.map((occurrence) => occurrence.kind));
	const outcomes = new Set(occurrences.flatMap((occurrence) => (occurrence.row === 'money' ? [occurrence.transfer.outcome] : [])));
	const hasManual = occurrences.some(isManualOccurrence);

	return (
		<span className={styles.legend}>
			{KIND_ORDER.filter((kind) => kinds.has(kind)).map((kind) => (
				<span key={kind} data-kind={kind}>
					{copy.kinds[kind]}
				</span>
			))}
			{outcomes.has('fail') ? <span data-mark="fail">{copy.marks.fail}</span> : null}
			{outcomes.has('info') ? <span data-mark="info">{copy.marks.info}</span> : null}
			{hasManual ? <span data-mark="manual">{copy.marks.manual}</span> : null}
			{hasCure ? <span data-mark="cure">{copy.marks.cure}</span> : null}
		</span>
	);
}
