import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
    browserLanguage,
    isInterfaceLanguage,
    languageFromBooks,
    readStoredLanguage,
    storeLanguage,
} from '@/shared/lib/interfaceLanguage';

describe('interfaceLanguage', () => {
    beforeEach(() => {
        window.localStorage.clear();
    });

    it('recognises only the two locales the interface speaks', () => {
        expect(isInterfaceLanguage('ru')).toBe(true);
        expect(isInterfaceLanguage('en')).toBe(true);
        expect(isInterfaceLanguage('uk')).toBe(false);
        expect(isInterfaceLanguage('')).toBe(false);
        expect(isInterfaceLanguage(undefined)).toBe(false);
    });

    it('round-trips a stored choice', () => {
        expect(readStoredLanguage()).toBeNull();
        storeLanguage('ru');
        expect(readStoredLanguage()).toBe('ru');
    });

    it('ignores a stored value it cannot honour', () => {
        window.localStorage.setItem('interfaceLang', 'uk');
        expect(readStoredLanguage()).toBeNull();
    });

    it('reads the browser, defaulting to English', () => {
        vi.spyOn(navigator, 'language', 'get').mockReturnValue('ru-RU');
        expect(browserLanguage()).toBe('ru');
        vi.spyOn(navigator, 'language', 'get').mockReturnValue('uk-UA');
        expect(browserLanguage()).toBe('en');
    });

    // This is the rule the application has always followed, and the backfill
    // preserves it so nobody's interface moves on the day this ships.
    it('derives the old behaviour from a books language', () => {
        expect(languageFromBooks('ru')).toBe('ru');
        expect(languageFromBooks('uk')).toBe('en');
        expect(languageFromBooks(undefined)).toBe('en');
    });
});
