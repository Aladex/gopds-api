// i18n.ts
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
// Import your translation files or objects
import translationEN from './locales/en/translation.json';
import translationRU from './locales/ru/translation.json';

const resources = {
    en: {
        translation: translationEN,
    },
    ru: {
        translation: translationRU,
    },
};

i18n
    .use(initReactI18next) // Passes i18n down to react-i18next
    .init({
        resources,
        lng: 'en', // language to use
        keySeparator: false, // we do not use keys in form messages.welcome
        interpolation: {
            escapeValue: false, // react already safes from xss
        },
    });

export default i18n;