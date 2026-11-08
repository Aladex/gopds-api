/**
 * The file a reader picked inside a card, remembered in their browser only and
 * keyed by the card. Storage can be missing or throw (a private window, blocked
 * site data), and that is no choice rather than an error: every access is
 * guarded, and the card opens on its default.
 */

const storageKey = (key: string) => `booksdump:variant:${key}`;

/** The remembered file of the card, or null. */
export function readRememberedVariant(key: string): number | null {
    try {
        const value = window.localStorage.getItem(storageKey(key));
        if (value === null) return null;
        const id = Number(value);
        return Number.isInteger(id) && id > 0 ? id : null;
    } catch {
        return null;
    }
}

/** Remembers the file the reader picked in the card. */
export function rememberVariant(key: string, bookID: number): void {
    try {
        window.localStorage.setItem(storageKey(key), String(bookID));
    } catch {
        // Nowhere to keep it: the card opens on its default next time.
    }
}

/** Forgets the card's remembered file. */
export function forgetVariant(key: string): void {
    try {
        window.localStorage.removeItem(storageKey(key));
    } catch {
        // Nothing to forget where nothing could be kept.
    }
}
