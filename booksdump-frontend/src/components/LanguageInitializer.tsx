import React, { useEffect, useRef } from 'react';
import { useAuth } from "../context/AuthContext";
import { useTranslation } from 'react-i18next';

const LanguageInitializer: React.FC<{ onLanguageLoaded: () => void }> = ({ onLanguageLoaded }) => {
    const { user, isLoaded } = useAuth();
    const { i18n } = useTranslation();
    const hasLoadedRef = useRef(false);
    const lastUserRef = useRef(user);

    useEffect(() => {
        // Ждем пока AuthContext полностью загрузится
        if (!isLoaded) {
            return;
        }

        // Проверяем, изменился ли пользователь
        const userChanged = lastUserRef.current !== user;
        if (userChanged) {
            hasLoadedRef.current = false;
            lastUserRef.current = user;
        }

        // Предотвращаем повторные вызовы только если пользователь не изменился
        if (hasLoadedRef.current && !userChanged) {
            return;
        }

        const setLanguage = async () => {
            let language = 'en'; // Default language

            if (user && user.books_lang) {
                // Пользователь загружен и у него есть настройки языка
                language = user.books_lang === 'ru' ? 'ru' : 'en';
            } else if (!user) {
                // Пользователь не авторизован - используем браузерный язык
                const browserLanguage = navigator.language;
                if (browserLanguage.startsWith('ru')) {
                    language = 'ru';
                }
            }

            await i18n.changeLanguage(language);
            hasLoadedRef.current = true;
            onLanguageLoaded();
        };

        setLanguage();
    }, [user, isLoaded, i18n, onLanguageLoaded]);

    return null;
};

export default LanguageInitializer;