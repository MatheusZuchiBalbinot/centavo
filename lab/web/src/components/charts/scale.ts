const NICE_STEP_MANTISSAS = [1, 2, 5, 10] as const;
const TARGET_TICK_COUNT = 4;

export function niceTicks(max: number): readonly number[] {
	if (max <= 0) {
		return [0, 1];
	}

	const roughStep = max / TARGET_TICK_COUNT;
	const magnitude = 10 ** Math.floor(Math.log10(roughStep));
	const mantissa = NICE_STEP_MANTISSAS.find((candidate) => candidate * magnitude >= roughStep) ?? 10;
	const step = mantissa * magnitude;
	const count = Math.ceil(max / step);

	return Array.from({ length: count + 1 }, (_, index) => index * step);
}

export function scaleLinear(domainMin: number, domainMax: number, rangeMin: number, rangeMax: number): (value: number) => number {
	const span = domainMax - domainMin || 1;

	return (value) => rangeMin + ((value - domainMin) / span) * (rangeMax - rangeMin);
}

// The axis covers this share of the values; the few above it are cut at the
// top instead of squashing everything else flat.
const SCALE_PERCENTILE = 0.98;
// Room above the covered values, so the line never touches the top.
const SCALE_HEADROOM = 1.15;

// Not so high that a lone spike, such as the burst that creates the round's
// accounts in its first second, flattens everything else.
export function scaleMaxOf(values: readonly number[]): number {
	if (values.length === 0) {
		return 0;
	}

	const sorted = [...values].sort((a, b) => a - b);
	const covered = sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * SCALE_PERCENTILE))] ?? 0;

	return Math.max(covered * SCALE_HEADROOM, values.at(-1) ?? 0);
}
