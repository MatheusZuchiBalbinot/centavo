import { useState } from 'react';

import type { RoundId } from '../../../api/ids';
import { stopRound } from '../../../api/lab';
import { Button } from '../../../components/ui/Button';
import { errorMessage } from '../../../domain/apiErrors';
import { useToasts } from '../../../hooks/toastContext';
import { useLabReading } from '../../../hooks/useLabReading';
import { messages } from '../../../i18n/pt-BR';
import styles from './RoundPage.module.css';
import StopRoundDialog from './StopRoundDialog';

type RoundActionsProps = { readonly round: RoundId; readonly isFinished: boolean };

// RoundActions copies the round's link and, while this round is the one the
// lab runs, offers to stop it.
export default function RoundActions({ round, isFinished }: RoundActionsProps): React.JSX.Element {
  const lab = useLabReading();
  const { push } = useToasts();
  const [isStopping, setStopping] = useState(false);

  const isThisRoundRunning = lab.kind === 'ready' && lab.state.status !== 'idle' && lab.state.round === round;
  const isLabStopping = lab.kind === 'ready' && lab.state.status === 'stopping';
  const canStop = isThisRoundRunning && !isFinished;

  async function handleCopyLink(): Promise<void> {
    try {
      await navigator.clipboard.writeText(window.location.href);
      push({ tone: 'info', title: messages.round.linkCopied });
    } catch {
      // The browser refused the clipboard (no permission, or not a secure
      // context); the address bar still has the link.
    }
  }

  async function handleStop(): Promise<void> {
    setStopping(true);

    const result = await stopRound();

    if (!result.ok) {
      setStopping(false);
      push({ tone: 'error', title: errorMessage(result.error) });

      return;
    }

    push({ tone: 'info', title: messages.round.stopping });
  }

  return (
    <div className={styles.actionsRow}>
      <Button onClick={() => void handleCopyLink()}>{messages.round.copyLink}</Button>
      {canStop ? (
        <StopRoundDialog isStopping={isStopping || isLabStopping} onConfirm={() => void handleStop()} />
      ) : null}
    </div>
  );
}
