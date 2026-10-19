import { useEffect, useReducer, useState } from 'react';

import type { RoundId } from '../../../api/ids';
import { openRoundStream } from '../../../api/roundStream';
import { initialRoundState, reduceRound } from '../../../domain/roundState';
import type { RoundState } from '../../../domain/roundState';

export type RoundStream = { readonly state: RoundState; readonly isConnected: boolean };

// useRoundStream follows one round for as long as the page shows it.
export function useRoundStream(round: RoundId): RoundStream {
  const [state, dispatch] = useReducer(reduceRound, initialRoundState);
  const [isConnected, setConnected] = useState(true);

  useEffect(() => {
    return openRoundStream(round, { onMessage: dispatch, onConnectionChange: setConnected });
  }, [round]);

  return { state, isConnected };
}
