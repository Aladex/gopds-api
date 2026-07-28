/**
 * The language the application speaks, as opposed to the language of the books
 * it holds.
 *
 * These were one field until they were not: a reader who wanted Ukrainian books
 * got an English interface, because the catalogue filter doubled as the locale
 * and every value but `ru` fell through to English. They are two different
 * questions — one of two answers about the application, one of forty about the
 * shelf — and this module owns the first.
 */

export type InterfaceLanguage = 'ru' | 'en';

export const INTERFACE_LANGUAGES: readonly InterfaceLanguage[] = ['ru', 'en'];

const STORAGE_KEY = 'interfaceLang';

export function isInterfaceLanguage(value: unknown): value is InterfaceLanguage {
    return value === 'ru' || value === 'en';
}

/**
 * Storage is reached through `window` and guarded on every use.
 *
 * Private browsing and blocked third-party storage do not return null, they
 * throw — on access as well as on write — and a locale preference is not worth
 * failing to render over.
 */
function storage(): Storage | null {
    try {
        return window.localStorage ?? null;
    } catch {
        // Private browsing and blocked storage throw on access rather than
        // returning null, and neither is a reason to fail to render.
        return null;
    }
}

/** The choice this browser remembers, or null if there is none worth honouring. */
export function readStoredLanguage(): InterfaceLanguage | null {
    try {
        const stored = storage()?.getItem(STORAGE_KEY);
        return isInterfaceLanguage(stored) ? stored : null;
    } catch {
        return null;
    }
}

export function storeLanguage(language: InterfaceLanguage): void {
    try {
        storage()?.setItem(STORAGE_KEY, language);
    } catch {
        // Nothing to do: the choice still holds for this page.
    }
}

export function browserLanguage(): InterfaceLanguage {
    return navigator.language?.startsWith('ru') ? 'ru' : 'en';
}

/**
 * What the interface language used to be, derived from the books language.
 *
 * Used once per account, to write down the behaviour that was already in force
 * before the two settings came apart.
 */
export function languageFromBooks(booksLang: string | undefined): InterfaceLanguage {
    return booksLang === 'ru' ? 'ru' : 'en';
}
