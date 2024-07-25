// src/components/LanguageInitializer.tsx
import React, { useEffect } from 'react';
import { useAuth } from "../context/AuthContext";
import { useTranslation } from 'react-i18next';

const LanguageInitializer: React.FC<{ onLanguageLoaded: () => void }> = ({ onLanguageLoaded }) => {
    const { user } = useAuth();
    const { i18n } = useTranslation();

    useEffect(() => {
        if (user) {
            const language = user.books_lang === 'ru' ? 'ru' : 'en';
            i18n.changeLanguage(language).then(() => {
                onLanguageLoaded();
            });
        }
    }, [user, i18n, onLanguageLoaded]);

    return null;
};

export default LanguageInitializer;