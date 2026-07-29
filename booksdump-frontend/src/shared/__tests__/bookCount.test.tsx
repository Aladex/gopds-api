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
