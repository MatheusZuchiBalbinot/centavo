import { useEffect, useState } from 'react';

import type { RoundId } from '../../../api/ids';
import { fetchRounds, type RoundSummary } from '../../../api/rounds';
import { isControl } from '../../../domain/scoreboard';

const ROUNDS_READ = 100;

export function useControlRound(scenario: string, isRobust: boolean): RoundId | null {
	const [control, setControl] = useState<RoundId | null>(null);

	useEffect(() => {
		if (!isRobust) {
			return;
		}

		const controller = new AbortController();

		async function load(): Promise<void> {
			const result = await fetchRounds({ limit: ROUNDS_READ }, controller.signal);

			if (!result.ok || controller.signal.aborted) {
				return;
			}

			setControl(result.data.rounds.find(isControlOf(scenario))?.round ?? null);
		}

		void load();

		return () => {
			controller.abort();
		};
	}, [scenario, isRobust]);

	return control;
}

function isControlOf(scenario: string): (round: RoundSummary) => boolean {
	return (round) => round.scenario === scenario && isControl(round);
}
