import { useSearchParams } from 'react-router';

import { isBet, type Bet } from '../../../domain/bets';
import { formatClock } from '../../../domain/duration';
import { parseClock } from '../../../domain/playback';
import { useViewMode } from '../../../hooks/viewModeContext';
import { QUERY } from '../../../routes';

// The arena's query values: open, or closed on purpose in Presentation mode.
const ARENA_OPEN = '1';
const ARENA_CLOSED = '0';
const SURPRISE = '1';
const MS_PER_SECOND = 1_000;
const START = 0;

export type ArenaQuery = {
	readonly isOpen: boolean;
	readonly isSurprise: boolean;
	readonly bet: Bet | null;
	// From ?t=m:ss; null follows the round as it is.
	readonly replayFromMs: number | null;
	readonly open: () => void;
	readonly close: () => void;
	readonly watchAgain: () => void;
	readonly keepMoment: (positionMs: number) => void;
};

// The arena's state lives in the address, so a copied link opens the same
// view: the arena, the surprise and its bet, and the moment of a replay.
export function useArenaQuery(): ArenaQuery {
	const [searchParams, setSearchParams] = useSearchParams();
	const isPresenting = useViewMode().mode === 'presentation';
	const arena = searchParams.get(QUERY.arena);
	const bet = searchParams.get(QUERY.bet);

	function update(changes: Readonly<Record<string, string | null>>): void {
		const next = new URLSearchParams(searchParams);

		for (const [key, value] of Object.entries(changes)) {
			if (value === null) {
				next.delete(key);
			} else {
				next.set(key, value);
			}
		}

		setSearchParams(next, { replace: true });
	}

	return {
		isOpen: arena === ARENA_OPEN || (isPresenting && arena !== ARENA_CLOSED),
		isSurprise: searchParams.get(QUERY.surprise) === SURPRISE,
		bet: isBet(bet) ? bet : null,
		replayFromMs: parseClock(searchParams.get(QUERY.moment)),
		open: () => update({ [QUERY.arena]: ARENA_OPEN }),
		close: () => update({ [QUERY.arena]: ARENA_CLOSED, [QUERY.moment]: null }),
		watchAgain: () => update({ [QUERY.arena]: ARENA_OPEN, [QUERY.moment]: formatClock(START) }),
		keepMoment: (positionMs) => update({ [QUERY.moment]: formatClock(positionMs / MS_PER_SECOND) }),
	};
}
