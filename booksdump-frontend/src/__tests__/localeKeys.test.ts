import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

import en from '@/locales/en/translation.json';
import ru from '@/locales/ru/translation.json';

// A key dropped from the locales fails nowhere: t() hands the key back and the
// reader sees `typeToSearch` where a sentence should be, in whatever screen still
// asks for it. Tests stub t(), so only reading the source can see this.

const root = join(import.meta.dirname, '..');

const sources = (dir: string): string[] =>
    readdirSync(dir).flatMap((entry) => {
        const path = join(dir, entry);
        if (statSync(path).isDirectory()) {
            return entry === '__tests__' ? [] : sources(path);
        }
        return /\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry) ? [path] : [];
    });

/** Every key written out as `t('key')` with no default text to fall back on. */
const askedFor = sources(root).flatMap((path) =>
    [...readFileSync(path, 'utf8').matchAll(/\bt\(\s*'([^']+)'\s*[,)]/g)].map((m) => m[1]),
);

// In the source and in no locale since before this test existed.
const KNOWN_MISSING = new Set(['unknownAddDate']);

const has = (locale: Record<string, string>, key: string) =>
    key in locale || `${key}_one` in locale;

describe('the keys the source asks for', () => {
    it('finds the keys it is reading for', () => {
        expect(askedFor.length).toBeGreaterThan(500);
        expect(askedFor).toContain('typeToSearch');
    });

    it.each([
        ['ru', ru],
        ['en', en],
    ])('are all in %s', (_, locale) => {
        const missing = [...new Set(askedFor)].filter(
            (key) => !has(locale, key) && !KNOWN_MISSING.has(key),
        );

        expect(missing).toEqual([]);
    });
});
