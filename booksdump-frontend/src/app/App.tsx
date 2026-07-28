import React, { memo, useMemo } from 'react';
import { AuthorProvider } from '@/context/AuthorContext';
import { BookConversionProvider } from '@/context/BookConversionContext';
import { FavProvider } from "@/context/FavContext";
import { SearchBarProvider } from '@/context/SearchBarContext';
import publicRoutes from '@/app/routes/publicRoutes';
import privateRoutes from '@/app/routes/privateRoutes';
import adminRoutes from "@/app/routes/adminRoutes";
import notFoundRoutes from "@/app/routes/notFoundRoutes";
import { InterfaceLanguageProvider } from '@/context/InterfaceLanguageContext';
import { useAuth } from '@/context/AuthContext';
import { Routes, Route, Navigate } from 'react-router-dom';
import useAuthWebSocket from '@/shared/hooks/useAuthWebSocket';
import AppSkeleton from '@/shared/components/AppSkeleton';
import { Toaster } from '@/shared/ui/sonner';

const App: React.FC<{ isAuthenticated: boolean }> = memo(({ isAuthenticated }) => {
    // Using WebSocket inside BookConversionProvider
    useAuthWebSocket("/api/ws", isAuthenticated);

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
    const { isLoaded, isAuthenticated } = useAuth();

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

    return (
        <>
            {/*
              The locale resolves from storage or the browser without asking
              anyone, so there is nothing to wait for beyond the account the
              application already waits on.
            */}
            <InterfaceLanguageProvider>
                {!isLoaded ? <AppSkeleton /> : providers}
            </InterfaceLanguageProvider>
            {/* One toaster for the whole application; anything can call toast(). */}
            <Toaster position="bottom-right" />
        </>
    );
};

export default AppWrapper;
