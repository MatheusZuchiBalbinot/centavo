// Keys of what the panel keeps in the browser, per person; nothing in it is
// sensitive or needed by the lab.
export const STORAGE_KEYS = {
	viewMode: 'centavo.viewMode',
	sound: 'centavo.sound',
	bets: 'centavo.bets',
} as const;

// readStored returns the stored value when the guard accepts it; storage can
// be missing, blocked or hold anything.
export function readStored<T>(key: string, guard: (value: unknown) => value is T): T | null {
	try {
		const raw = window.localStorage.getItem(key);

		if (raw === null) {
			return null;
		}

		const value: unknown = JSON.parse(raw);

		return guard(value) ? value : null;
	} catch {
		return null;
	}
}

export function writeStored(key: string, value: unknown): void {
	try {
		window.localStorage.setItem(key, JSON.stringify(value));
	} catch {
		// Storage is a convenience: a blocked one only means the choice is not
		// remembered next time.
	}
}
