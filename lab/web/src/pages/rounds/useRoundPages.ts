import { useCallback, useEffect, useState } from 'react';

import type { RoundId } from '../../api/ids';
import { fetchRounds, type RoundSummary } from '../../api/rounds';

const PAGE_SIZE = 25;

export type RoundPages =
	| { readonly kind: 'loading' }
	| { readonly kind: 'failed' }
	| {
			readonly kind: 'ready';
			readonly rounds: readonly RoundSummary[];
			readonly hasMore: boolean;
			readonly isLoadingMore: boolean;
			readonly loadMore: () => void;
	  };

// useRoundPages reads the finished rounds a page at a time, newest first.
export function useRoundPages(): RoundPages {
	const [rounds, setRounds] = useState<readonly RoundSummary[] | null>(null);
	const [cursor, setCursor] = useState<RoundId | null>(null);
	const [isLoadingMore, setLoadingMore] = useState(false);
	const [hasFailed, setFailed] = useState(false);

	useEffect(() => {
		const controller = new AbortController();

		async function loadFirst(): Promise<void> {
			const result = await fetchRounds({ limit: PAGE_SIZE }, controller.signal);

			if (controller.signal.aborted) {
				return;
			}

			if (!result.ok) {
				setFailed(true);

				return;
			}

			setRounds(result.data.rounds);
			setCursor(result.data.nextCursor);
		}

		void loadFirst();

		return () => {
			controller.abort();
		};
	}, []);

	const loadMore = useCallback(() => {
		async function loadNext(): Promise<void> {
			if (cursor === null) {
				return;
			}

			setLoadingMore(true);

			const result = await fetchRounds({ limit: PAGE_SIZE, cursor });

			setLoadingMore(false);

			if (!result.ok) {
				setFailed(true);

				return;
			}

			setRounds((current) => [...(current ?? []), ...result.data.rounds]);
			setCursor(result.data.nextCursor);
		}

		void loadNext();
	}, [cursor]);

	if (hasFailed && rounds === null) {
		return { kind: 'failed' };
	}

	if (rounds === null) {
		return { kind: 'loading' };
	}

	return { kind: 'ready', rounds, hasMore: cursor !== null, isLoadingMore, loadMore };
}
