import {
    forgetVariant,
    readRememberedVariant,
    rememberVariant,
} from '@/features/catalogue/variants/rememberedVariant';

/*
 * The reader's choice lives in their browser only, one entry per card. Storage
 * that throws (a private window, blocked site data) is no choice and no error.
 */

afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
});

describe('the remembered variant', () => {
    it('reads back what was written, under the card key', () => {
        rememberVariant('k-1', 42);
        expect(readRememberedVariant('k-1')).toBe(42);
        expect(localStorage.getItem('booksdump:variant:k-1')).toBe('42');
        expect(readRememberedVariant('k-2')).toBeNull();
    });

    it('is null when nothing is stored, or something unreadable is', () => {
        expect(readRememberedVariant('none')).toBeNull();
        localStorage.setItem('booksdump:variant:junk', 'abc');
        expect(readRememberedVariant('junk')).toBeNull();
    });

    it('forgets', () => {
        rememberVariant('k', 7);
        forgetVariant('k');
        expect(readRememberedVariant('k')).toBeNull();
    });

    it('reads null when storage throws', () => {
        vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => {
            throw new Error('SecurityError');
        });
        expect(() => readRememberedVariant('k')).not.toThrow();
        expect(readRememberedVariant('k')).toBeNull();
    });

    it('does not throw when writing or forgetting throws', () => {
        vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => {
            throw new Error('QuotaExceededError');
        });
        vi.spyOn(window.localStorage, 'removeItem').mockImplementation(() => {
            throw new Error('SecurityError');
        });
        expect(() => rememberVariant('k', 1)).not.toThrow();
        expect(() => forgetVariant('k')).not.toThrow();
    });

    it('copes with a window whose storage cannot even be reached', () => {
        const saved = Object.getOwnPropertyDescriptor(window, 'localStorage');
        Object.defineProperty(window, 'localStorage', {
            configurable: true,
            get() {
                throw new Error('SecurityError');
            },
        });
        try {
            expect(readRememberedVariant('k')).toBeNull();
            expect(() => rememberVariant('k', 1)).not.toThrow();
            expect(() => forgetVariant('k')).not.toThrow();
        } finally {
            if (saved) Object.defineProperty(window, 'localStorage', saved);
        }
    });
});
