import {
    getLanguageInfo,
    getLanguageDisplay,
    getAllLanguagesInfo,
    isLanguageSupported,
    filterSupportedLanguages,
    getLanguageDisplaySafe,
    languageMapping
} from './languageUtils';

describe('languageUtils', () => {
    describe('getLanguageInfo', () => {
        it('should return correct language info for supported languages', () => {
            const result = getLanguageInfo('ru');
            expect(result).toEqual({
                code: 'ru',
                name: 'Русский',
                flag: '🇷🇺'
            });
        });

        it('should return correct language info for english', () => {
            const result = getLanguageInfo('en');
            expect(result).toEqual({
                code: 'en',
                name: 'English',
                flag: '🇬🇧'
            });
        });

        it('should handle uppercase language codes', () => {
            const result = getLanguageInfo('RU');
            expect(result).toEqual({
                code: 'ru',
                name: 'Русский',
                flag: '🇷🇺'
            });
        });

        it('should handle language codes with whitespace', () => {
            const result = getLanguageInfo('  ru  ');
            expect(result).toEqual({
                code: 'ru',
                name: 'Русский',
                flag: '🇷🇺'
            });
        });

        it('should return fallback for unsupported languages', () => {
            const result = getLanguageInfo('xyz');
            expect(result).toEqual({
                code: 'xyz',
                name: 'XYZ',
                flag: '🏳️'
            });
        });

        it('should handle empty string', () => {
            const result = getLanguageInfo('');
            expect(result).toEqual({
                code: 'unknown',
                name: 'UNKNOWN',
                flag: '🏳️'
            });
        });

        it('should handle null and undefined', () => {
            const resultNull = getLanguageInfo(null as any);
            const resultUndefined = getLanguageInfo(undefined as any);
            
            expect(resultNull).toEqual({
                code: 'unknown',
                name: 'UNKNOWN',
                flag: '🏳️'
            });
            
            expect(resultUndefined).toEqual({
                code: 'unknown',
                name: 'UNKNOWN',
                flag: '🏳️'
            });
        });
    });

    describe('getLanguageDisplay', () => {
        it('should return formatted display for supported languages', () => {
            expect(getLanguageDisplay('ru')).toBe('🇷🇺 Русский');
            expect(getLanguageDisplay('en')).toBe('🇬🇧 English');
            expect(getLanguageDisplay('de')).toBe('🇩🇪 Deutsch');
        });

        it('should return formatted display for unsupported languages', () => {
            expect(getLanguageDisplay('xyz')).toBe('🏳️ XYZ');
        });

        it('should handle empty string', () => {
            expect(getLanguageDisplay('')).toBe('🏳️ UNKNOWN');
        });
    });

    describe('isLanguageSupported', () => {
        it('should return true for supported languages', () => {
            expect(isLanguageSupported('ru')).toBe(true);
            expect(isLanguageSupported('en')).toBe(true);
            expect(isLanguageSupported('de')).toBe(true);
            expect(isLanguageSupported('fr')).toBe(true);
            expect(isLanguageSupported('zh')).toBe(true);
        });

        it('should return false for unsupported languages', () => {
            expect(isLanguageSupported('xyz')).toBe(false);
            expect(isLanguageSupported('unknown')).toBe(false);
            expect(isLanguageSupported('test123')).toBe(false);
        });

        it('should handle case insensitive check', () => {
            expect(isLanguageSupported('RU')).toBe(true);
            expect(isLanguageSupported('En')).toBe(true);
            expect(isLanguageSupported('DE')).toBe(true);
        });

        it('should handle whitespace', () => {
            expect(isLanguageSupported('  ru  ')).toBe(true);
            expect(isLanguageSupported(' en ')).toBe(true);
        });

        it('should return false for empty, null, undefined', () => {
            expect(isLanguageSupported('')).toBe(false);
            expect(isLanguageSupported(null as any)).toBe(false);
            expect(isLanguageSupported(undefined as any)).toBe(false);
        });
    });

    describe('filterSupportedLanguages', () => {
        it('should filter array to only include supported languages', () => {
            const input = ['ru', 'en', 'xyz', 'de', 'unknown', 'fr'];
            const result = filterSupportedLanguages(input);
            expect(result).toEqual(['ru', 'en', 'de', 'fr']);
        });

        it('should return empty array for all unsupported languages', () => {
            const input = ['xyz', 'unknown', 'test123'];
            const result = filterSupportedLanguages(input);
            expect(result).toEqual([]);
        });

        it('should handle empty array', () => {
            const result = filterSupportedLanguages([]);
            expect(result).toEqual([]);
        });

        it('should handle case insensitive filtering', () => {
            const input = ['RU', 'En', 'XYZ', 'DE'];
            const result = filterSupportedLanguages(input);
            expect(result).toEqual(['RU', 'En', 'DE']);
        });
    });

    describe('getLanguageDisplaySafe', () => {
        it('should return formatted display for supported languages', () => {
            expect(getLanguageDisplaySafe('ru')).toBe('🇷🇺 Русский');
            expect(getLanguageDisplaySafe('en')).toBe('🇬🇧 English');
        });

        it('should return null for unsupported languages', () => {
            expect(getLanguageDisplaySafe('xyz')).toBe(null);
            expect(getLanguageDisplaySafe('unknown')).toBe(null);
        });

        it('should return null for empty, null, undefined', () => {
            expect(getLanguageDisplaySafe('')).toBe(null);
            expect(getLanguageDisplaySafe(null as any)).toBe(null);
            expect(getLanguageDisplaySafe(undefined as any)).toBe(null);
        });
    });

    describe('getAllLanguagesInfo', () => {
        it('should return array of all language info objects', () => {
            const result = getAllLanguagesInfo();
            expect(Array.isArray(result)).toBe(true);
            expect(result.length).toBeGreaterThan(0);
            
            // Check that each item has required properties
            result.forEach(lang => {
                expect(lang).toHaveProperty('code');
                expect(lang).toHaveProperty('name');
                expect(lang).toHaveProperty('flag');
                expect(typeof lang.code).toBe('string');
                expect(typeof lang.name).toBe('string');
                expect(typeof lang.flag).toBe('string');
            });
        });

        it('should include common languages', () => {
            const result = getAllLanguagesInfo();
            const codes = result.map(lang => lang.code);
            
            expect(codes).toContain('ru');
            expect(codes).toContain('en');
            expect(codes).toContain('de');
            expect(codes).toContain('fr');
            expect(codes).toContain('es');
        });
    });

    describe('languageMapping consistency', () => {
        it('should have consistent code property in each language object', () => {
            Object.entries(languageMapping).forEach(([key, value]) => {
                expect(value.code).toBe(key);
            });
        });

        it('should have all required properties for each language', () => {
            Object.values(languageMapping).forEach(lang => {
                expect(lang).toHaveProperty('code');
                expect(lang).toHaveProperty('name');
                expect(lang).toHaveProperty('flag');
                expect(typeof lang.code).toBe('string');
                expect(typeof lang.name).toBe('string');
                expect(typeof lang.flag).toBe('string');
                expect(lang.code.length).toBeGreaterThan(0);
                expect(lang.name.length).toBeGreaterThan(0);
                expect(lang.flag.length).toBeGreaterThan(0);
            });
        });

        it('should not have duplicate language codes', () => {
            const codes = Object.keys(languageMapping);
            const uniqueCodes = Array.from(new Set(codes));
            expect(codes.length).toBe(uniqueCodes.length);
        });
    });

    describe('Real world scenarios', () => {
        it('should handle typical library language codes', () => {
            const libraryLanguages = ['ru', 'en', 'de', 'fr', 'es', 'it', 'pl', 'uk'];
            
            libraryLanguages.forEach(lang => {
                expect(isLanguageSupported(lang)).toBe(true);
                const info = getLanguageInfo(lang);
                expect(info.code).toBe(lang);
                expect(info.name).toBeTruthy();
                expect(info.flag).toBeTruthy();
                expect(info.flag).not.toBe('🏳️'); // Should not be fallback flag
            });
        });

        it('should filter mixed language arrays correctly', () => {
            const mixedLanguages = ['ru', 'xyz', 'en', 'unknown', 'de', 'test123', 'fr'];
            const filtered = filterSupportedLanguages(mixedLanguages);
            const expected = ['ru', 'en', 'de', 'fr'];
            expect(filtered).toEqual(expected);
        });

        it('should provide safe display for UI components', () => {
            const supportedLang = getLanguageDisplaySafe('ru');
            const unsupportedLang = getLanguageDisplaySafe('xyz');
            
            expect(supportedLang).toBeTruthy();
            expect(supportedLang).toContain('🇷🇺');
            expect(supportedLang).toContain('Русский');
            
            expect(unsupportedLang).toBe(null);
        });
    });

    describe('Edge cases', () => {
        it('should handle various input types gracefully', () => {
            const inputs = [
                '',
                ' ',
                'RU',
                'ru',
                '  RU  ',
                'unknown_language',
                '123',
                'test-lang'
            ];

            inputs.forEach(input => {
                expect(() => getLanguageInfo(input)).not.toThrow();
                expect(() => getLanguageDisplay(input)).not.toThrow();
                expect(() => isLanguageSupported(input)).not.toThrow();
                expect(() => getLanguageDisplaySafe(input)).not.toThrow();
            });
        });

        it('should normalize language codes consistently', () => {
            const variations = ['ru', 'RU', 'Ru', 'rU', '  ru  ', '  RU  '];
            
            variations.forEach(variation => {
                const info = getLanguageInfo(variation);
                expect(info.code).toBe('ru');
                expect(info.name).toBe('Русский');
                expect(info.flag).toBe('🇷🇺');
            });
        });
    });
});
