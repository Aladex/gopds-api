import React, { useEffect } from 'react';
import { useAuth } from "../context/AuthContext";
import { useTranslation } from 'react-i18next';

const LanguageInitializer: React.FC<{ onLanguageLoaded: () => void }> = ({ onLanguageLoaded }) => {
    const { user } = useAuth();
    const { i18n } = useTranslation();

    useEffect(() => {
        const setLanguage = async () => {
            let language = 'en'; // Default language

            if (user) {
                language = user.books_lang === 'ru' ? 'ru' : 'en';
            } else {
                const browserLanguage = navigator.language;
                if (browserLanguage.startsWith('ru')) {
                    language = 'ru';
                }
            }

            await i18n.changeLanguage(language);
            onLanguageLoaded();
        };

        setLanguage();
    }, [user, i18n, onLanguageLoaded]);

    return null;
};

export default LanguageInitializer;