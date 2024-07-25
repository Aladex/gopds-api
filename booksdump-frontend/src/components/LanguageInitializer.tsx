// src/components/LanguageInitializer.tsx
import React, { useEffect } from 'react';
import { useAuth } from "../context/AuthContext";
import { useTranslation } from 'react-i18next';

const LanguageInitializer: React.FC = () => {
    const { user } = useAuth();
    const { i18n } = useTranslation();

    useEffect(() => {
        if (user) {
            const language = user.books_lang === 'ru' ? 'ru' : 'en';
            i18n.changeLanguage(language).then(r => console.log(r));
        }
    }, [user, i18n]);

    return null;
};

export default LanguageInitializer;
