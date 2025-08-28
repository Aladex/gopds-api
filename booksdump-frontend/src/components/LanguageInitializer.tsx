import React, { useEffect, useRef } from 'react';
import { useAuth } from "../context/AuthContext";
import { useTranslation } from 'react-i18next';

const LanguageInitializer: React.FC<{ onLanguageLoaded: () => void }> = ({ onLanguageLoaded }) => {
    const { user, isLoaded } = useAuth();
    const { i18n } = useTranslation();
    const hasLoadedRef = useRef(false);

    useEffect(() => {
        // Ждем пока AuthContext полностью загрузится
        if (!isLoaded) return;

        // Предотвращаем повторные вызовы
        if (hasLoadedRef.current) return;

        const setLanguage = async () => {
            let language = 'en'; // Default language

            if (user && user.books_lang) {
                // Пользователь загружен и у него есть настройки языка
                language = user.books_lang === 'ru' ? 'ru' : 'en';
            } else {
                // Пользователь не авторизован или нет настроек - используем браузерный язык
                const browserLanguage = navigator.language;
                if (browserLanguage.startsWith('ru')) {
                    language = 'ru';
                }
            }

            console.log('Setting language:', language, 'for user:', user?.username || 'anonymous');
            await i18n.changeLanguage(language);
            hasLoadedRef.current = true;
            onLanguageLoaded();
        };

        setLanguage();
    }, [user, isLoaded, i18n, onLanguageLoaded]);

    return null;
};

export default LanguageInitializer;