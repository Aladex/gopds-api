import React, { memo, useMemo } from 'react';
import { AuthorProvider } from '@/context/AuthorContext';
import { BookConversionProvider } from '@/context/BookConversionContext';
import { FavProvider } from '@/context/FavContext';
import { SearchBarProvider } from '@/context/SearchBarContext';
import publicRoutes from '@/app/routes/publicRoutes';
import privateRoutes from '@/app/routes/privateRoutes';
import adminRoutes from '@/app/routes/adminRoutes';
import notFoundRoutes from '@/app/routes/notFoundRoutes';
import { InterfaceLanguageProvider } from '@/context/InterfaceLanguageContext';
import { useAuth } from '@/context/AuthContext';
import { Routes, Route, Navigate } from 'react-router';
import { WebSocketProvider } from '@/context/WebSocketContext';
import AppSkeleton from '@/shared/components/AppSkeleton';
import { Toaster } from '@/shared/ui/sonner';

const App: React.FC<{ isAuthenticated: boolean }> = memo(() => {
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

    // Memoize providers to avoid unnecessary rerenders.
    const providers = useMemo(
        () => (
            <FavProvider>
                <AuthorProvider>
                    <SearchBarProvider>
                        <BookConversionProvider>
                            {/* The one per-tab socket; every page's events ride it. */}
                            <WebSocketProvider isAuthenticated={isAuthenticated}>
                                <App isAuthenticated={isAuthenticated} />
                            </WebSocketProvider>
                        </BookConversionProvider>
                    </SearchBarProvider>
                </AuthorProvider>
            </FavProvider>
        ),
        [isAuthenticated],
    );

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
