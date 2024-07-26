import React, { useEffect } from 'react';
import { useAuth } from "../context/AuthContext";
import { useTranslation } from 'react-i18next';

const LanguageInitializer: React.FC<{ onLanguageLoaded: () => void }> = ({ onLanguageLoaded }) => {
    const { user } = useAuth();
    const { i18n } = useTranslation();

    useEffect(() => {
        const setLanguage = async () => {
            if (user) {
                const language = user.books_lang === 'ru' ? 'ru' : 'en';
                await i18n.changeLanguage(language);
            } else {
                await i18n.changeLanguage('en'); // Или другой язык по умолчанию
            }
            onLanguageLoaded();
        };

        setLanguage();
    }, [user, i18n, onLanguageLoaded]);

    return null;
};

export default LanguageInitializer;
