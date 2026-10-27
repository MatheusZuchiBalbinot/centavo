// A run of attacks, by their numbers in the round (1 is the first attack),
// that the page lights up across the chart and the feed.
export type AttackRange = { readonly from: number; readonly to: number };

export function overlaps(range: AttackRange | null, from: number, to: number): boolean {
	return range !== null && from <= range.to && to >= range.from;
}

// rangeOfLabel reads "7" or "10–14", the label the chart and the feed share.
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
