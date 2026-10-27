// Attack numbers in the round, 1 being the first; the chart and the feed
// light up the same range.
export type AttackRange = { readonly from: number; readonly to: number };

export function overlaps(range: AttackRange | null, from: number, to: number): boolean {
	return range !== null && from <= range.to && to >= range.from;
}

export function rangeOfLabel(label: string | null): AttackRange | null {
	if (label === null) {
		return null;
	}

	const [from, to] = label.split('–').map(Number);

	if (from === undefined || Number.isNaN(from)) {
		return null;
	}

	return { from, to: to ?? from };
}
