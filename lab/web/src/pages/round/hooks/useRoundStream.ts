import { useEffect, useReducer, useState } from 'react';

import type { RoundId } from '../../../api/ids';
import { openRoundStream } from '../../../api/roundStream';
import type { StreamMessage } from '../../../api/roundStream';
import { initialRoundState, reduceRound } from '../../../domain/roundState';
import type { RoundState } from '../../../domain/roundState';

export type RoundStream = {
  readonly state: RoundState;
  readonly isConnected: boolean;
  // lastMessageAt is when the stream last said anything, for "há 2 s".
  readonly lastMessageAt: number | null;
};

// useRoundStream follows one round for as long as the page shows it.
export function useRoundStream(round: RoundId): RoundStream {
  const [state, dispatch] = useReducer(reduceRound, initialRoundState);
  const [isConnected, setConnected] = useState(true);
  const [lastMessageAt, setLastMessageAt] = useState<number | null>(null);

  useEffect(() => {
    function handleMessage(message: StreamMessage): void {
      setLastMessageAt(Date.now());
      dispatch(message);
    }

    return openRoundStream(round, { onMessage: handleMessage, onConnectionChange: setConnected });
  }, [round]);

  return { state, isConnected, lastMessageAt };
}
