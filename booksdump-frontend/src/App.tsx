import React, { useState, memo, useMemo, useCallback, useEffect } from 'react';
import { AuthorProvider } from './context/AuthorContext';
import { BookConversionProvider } from './context/BookConversionContext';
import { FavProvider } from "./context/FavContext";
import { SearchBarProvider } from './context/SearchBarContext';
import publicRoutes from './routes/publicRoutes';
import privateRoutes from './routes/privateRoutes';
import adminRoutes from "./routes/adminRoutes";
import notFoundRoutes from "./routes/notFoundRoutes";
import LanguageInitializer from './components/LanguageInitializer';
import { useAuth } from './context/AuthContext';
import { Routes, Route, Navigate } from 'react-router-dom';
import useAuthWebSocket from './components/hooks/useAuthWebSocket';
import AppSkeleton from './components/common/AppSkeleton';

const App: React.FC<{ isAuthenticated: boolean }> = memo(({ isAuthenticated }) => {
    // Using WebSocket inside BookConversionProvider
    useAuthWebSocket("/api/books/ws", isAuthenticated);

    return (
        <Routes>
            <Route path="/" element={<Navigate to="/books/page/1" />} />
            {publicRoutes}
            {privateRoutes}
            {adminRoutes}
            {notFoundRoutes}
        </Routes>
    );
});

App.displayName = 'App';

const AppWrapper: React.FC = () => {
    const [isLanguageLoaded, setIsLanguageLoaded] = useState(false);
    const { isLoaded, isAuthenticated } = useAuth();

    // Используем useCallback для стабильной ссылки на функцию
    const handleLanguageLoaded = useCallback(() => {
        setIsLanguageLoaded(true);
    }, []);

    // Сброс состояния языка при изменении пользователя
    useEffect(() => {
        if (!isLoaded) {
            setIsLanguageLoaded(false);
        }
    }, [isLoaded]);

    // Мемоизируем провайдеры чтобы избежать ненужных перерендеров
    const providers = useMemo(() => (
        <FavProvider>
            <AuthorProvider>
                <SearchBarProvider>
                    <BookConversionProvider>
                        <App isAuthenticated={isAuthenticated} />
                    </BookConversionProvider>
                </SearchBarProvider>
            </AuthorProvider>
        </FavProvider>
    ), [isAuthenticated]);

    // Показываем спиннер если AuthContext не загружен или язык не инициализирован
    const showLoading = !isLoaded || !isLanguageLoaded;

    return (
        <>
            {/* LanguageInitializer должен работать только когда AuthContext загружен */}
            {isLoaded && (
                <LanguageInitializer onLanguageLoaded={handleLanguageLoaded} />
            )}
            {showLoading ? (
                <AppSkeleton />
            ) : (
                providers
            )}
        </>
    );
};

export default AppWrapper;
