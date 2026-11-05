import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from '@/locales/en/translation.json';
import ru from '@/locales/ru/translation.json';

// The count is rendered through i18next plurals, and Russian needs three forms
// where English needs two. These check the real catalogues, not a stub.
beforeAll(async () => {
    await i18n.use(initReactI18next).init({
        resources: { en: { translation: en }, ru: { translation: ru } },
        lng: 'en',
        interpolation: { escapeValue: false },
    });
});

describe('bookCount', () => {
    it.each([
        [1, '1 book'],
        [2, '2 books'],
        [176, '176 books'],
    ])('reads %i in English as %s', async (count, want) => {
        await i18n.changeLanguage('en');
        expect(i18n.t('bookCount', { count })).toBe(want);
    });

    it.each([
        [1, '1 книга'],
        [2, '2 книги'],
        [4, '4 книги'],
        [5, '5 книг'],
        [11, '11 книг'],
        [21, '21 книга'],
        [176, '176 книг'],
    ])('reads %i in Russian as %s', async (count, want) => {
        await i18n.changeLanguage('ru');
        expect(i18n.t('bookCount', { count })).toBe(want);
    });
});

describe('authorSearch.total', () => {
    it.each([
        [1, '1 author'],
        [2, '2 authors'],
        [38, '38 authors'],
    ])('reads %i in English as %s', async (count, want) => {
        await i18n.changeLanguage('en');
        expect(i18n.t('authorSearch.total', { count })).toBe(want);
    });

    it.each([
        [1, '1 автор'],
        [2, '2 автора'],
        [5, '5 авторов'],
        [11, '11 авторов'],
        [21, '21 автор'],
        [2840, '2840 авторов'],
    ])('reads %i in Russian as %s', async (count, want) => {
        await i18n.changeLanguage('ru');
        expect(i18n.t('authorSearch.total', { count })).toBe(want);
    });
});

describe('author search wording', () => {
    const keys = [
        'authorSearch.coauthors',
        'authorSearch.similarName',
        'authorSearch.alsoIn',
        'authorSearch.narrowDown',
        'authorSearch.narrowDownShort',
        'authorSearch.emptyHint',
        'authorSearch.searchBooks',
    ] as const;

    it.each(['en', 'ru'])('is there in %s', async (lng) => {
        await i18n.changeLanguage(lng);
        for (const key of keys) {
            expect(i18n.t(key, { name: 'x' })).not.toBe(key);
        }
    });

    it('puts the query in the empty result', async () => {
        await i18n.changeLanguage('ru');
        expect(i18n.t('noAuthorsFound', { query: 'шмурдяк' })).toContain('«шмурдяк»');
    });

    it('puts the matched name in the note', async () => {
        await i18n.changeLanguage('ru');
        expect(i18n.t('authorSearch.alsoIn', { name: 'Алекс' })).toBe('в книгах также: Алекс');
    });
});
