import { useEffect, useState } from 'react';

import type { ApiFailure } from '../../../api/client';
import { launchManualAttack, type ManualAttack } from '../../../api/lab';
import { errorMessage } from '../../../domain/apiErrors';
import type { ManualAttackControl, ManualAttackState } from '../../../domain/manualAttacks';
import { messages } from '../../../i18n/pt-BR';

const COOLDOWN_MS = 2_000;

export function useManualAttack(): ManualAttackControl {
	const [state, setState] = useState<ManualAttackState>({ status: 'ready', feedback: null });

	useEffect(() => {
		if (state.status !== 'cooling') {
			return;
		}

		const { feedback } = state;
		const timer = window.setTimeout(() => setState({ status: 'ready', feedback }), COOLDOWN_MS);

		return () => {
			window.clearTimeout(timer);
		};
	}, [state]);

	async function send(attack: ManualAttack): Promise<void> {
		setState({ status: 'sending' });

		const result = await launchManualAttack(attack);

		if (!result.ok) {
			setState({ status: 'ready', feedback: { tone: 'refused', text: refusalOf(result.error) } });

			return;
		}

		setState({ status: 'cooling', feedback: { tone: 'sent', text: messages.arena.manual.sent } });
	}

	function launch(attack: ManualAttack): void {
		void send(attack);
	}

	return { state, launch };
}

// The lab's rate_limited text speaks of rounds; here it is about attacks.
function refusalOf(failure: ApiFailure): string {
	if (failure.code === 'rate_limited') {
		return messages.arena.manual.tooMany;
	}

	return errorMessage(failure);
}
