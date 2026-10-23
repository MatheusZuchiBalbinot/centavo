import { useState } from 'react';

import type { AttackId } from '../api/ids';
import { startRound } from '../api/lab';
import { attackCopy } from '../domain/attacks';
import { errorMessage } from '../domain/apiErrors';
import { formatDuration } from '../domain/duration';
import { useToasts } from './toastContext';
import { format } from '../i18n/format';
import { messages } from '../i18n/pt-BR';

export type StartChoice = { readonly attack: AttackId; readonly durationSeconds: number };

type StartRound = { readonly isStarting: boolean; readonly start: (choice: StartChoice) => Promise<boolean> };

// useStartRound asks the lab for a round and says how it went in a toast.
export function useStartRound(): StartRound {
  const [isStarting, setStarting] = useState(false);
  const { push } = useToasts();

  async function start(choice: StartChoice): Promise<boolean> {
    setStarting(true);

    const result = await startRound(choice);

    setStarting(false);

    if (!result.ok) {
      push({ tone: 'error', title: errorMessage(result.error) });

      return false;
    }

    const values = { attack: attackCopy(choice.attack).name, duration: formatDuration(choice.durationSeconds) };
    push({ tone: 'info', title: format(messages.composer.started, values) });

    return true;
  }

  return { isStarting, start };
}
