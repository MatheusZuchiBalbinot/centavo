const NICE_STEP_MANTISSAS = [1, 2, 5, 10] as const;
const TARGET_TICK_COUNT = 4;

// niceTicks is a value axis from zero to a round number above max.
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

export function scaleLinear(
  domainMin: number,
  domainMax: number,
  rangeMin: number,
  rangeMax: number,
): (value: number) => number {
  const span = domainMax - domainMin || 1;

  return (value) => rangeMin + ((value - domainMin) / span) * (rangeMax - rangeMin);
}
