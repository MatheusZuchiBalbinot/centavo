import { Dices } from 'lucide-react';
import { useState } from 'react';
import { useNavigate } from 'react-router';

import type { Bet } from '../../domain/bets';
import { drawableAttacks, drawSurprise, reelOf } from '../../domain/surprise';
import type { SurpriseDraw } from '../../domain/surprise';
import { useAttackMenu } from '../../hooks/useAttackMenu';
import { useStartRound } from '../../hooks/useStartRound';
import { messages } from '../../i18n/pt-BR';
import { surpriseLivePath } from '../../routes';
import { Button } from '../ui/Button';
import DrawDialog from './DrawDialog';
import type { DrawSession } from './DrawDialog';

// How long the reel spins before it stops on the attack.
const SPIN_MS = 2_500;
const REDUCED_MOTION = '(prefers-reduced-motion: reduce)';

type SurpriseButtonProps = { readonly isBusy: boolean; readonly size?: 'regular' | 'large' };

// SurpriseButton draws an attack and a duration, counts down and starts the
// round, then follows it into the arena.
export default function SurpriseButton({ isBusy, size = 'regular' }: SurpriseButtonProps): React.JSX.Element {
  const menu = useAttackMenu();
  const { isStarting, start } = useStartRound();
  const navigate = useNavigate();
  const [session, setSession] = useState<DrawSession | null>(null);
  const [bet, setBet] = useState<Bet | null>(null);
  const isReady = menu.kind === 'ready';

  function newSession(): DrawSession | null {
    if (menu.kind !== 'ready') {
      return null;
    }

    const draw = drawSurprise(menu.menu, Math.random);

    if (draw === null) {
      return null;
    }

    const reel = reelOf(drawableAttacks(menu.menu), draw.attack, Math.random);
    // Without motion, the reel lands at once and only the count remains.
    const spinMs = window.matchMedia(REDUCED_MOTION).matches ? 0 : SPIN_MS;

    return { draw, reel, startedAtMs: Date.now(), spinMs };
  }

  function handleOpen(): void {
    setBet(null);
    setSession(newSession());
  }

  function handleRedraw(): void {
    setSession(newSession());
  }

  function handleCancel(): void {
    setSession(null);
  }

  async function startAndFollow(draw: SurpriseDraw): Promise<void> {
    const isStarted = await start(draw);

    setSession(null);

    if (isStarted) {
      await navigate(surpriseLivePath(bet));
    }
  }

  function handleReady(draw: SurpriseDraw): void {
    void startAndFollow(draw);
  }

  return (
    <>
      <Button variant="primary" size={size} onClick={handleOpen} disabled={isBusy || !isReady}>
        <Dices size={16} aria-hidden="true" />
        {messages.surprise.open}
      </Button>
      {session === null ? null : (
        <DrawDialog
          session={session}
          bet={bet}
          isStarting={isStarting}
          onBet={setBet}
          onRedraw={handleRedraw}
          onCancel={handleCancel}
          onReady={handleReady}
        />
      )}
    </>
  );
}
