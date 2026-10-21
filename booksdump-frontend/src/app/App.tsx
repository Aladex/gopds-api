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
import { Routes, Route, Navigate, useLocation } from 'react-router';
import { WebSocketProvider } from '@/context/WebSocketContext';
import AppSkeleton from '@/shared/components/AppSkeleton';
import { Toaster } from '@/shared/ui/sonner';
import { AuthRoomProvider } from '@/features/auth/scene/AuthRoom';

/**
 * RootRedirect sends the site's root to the first page of books, keeping the
 * query: links from outside — the Telegram bot's «Open profile» is
 * /?profile=telegram — say what to open there.
 */
export const RootRedirect: React.FC = () => {
    const { search } = useLocation();
    return <Navigate to={{ pathname: '/books/page/1', search }} replace />;
};

const App: React.FC<{ isAuthenticated: boolean }> = memo(() => {
    return (
        <Routes>
            <Route path="/" element={<RootRedirect />} />
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
                {/*
                  The reading room behind the public screens lives here, above
                  the routes and outside the providers that are rebuilt on
                  sign-in, so moving between those screens keeps one scene.
                */}
                <AuthRoomProvider>{!isLoaded ? <AppSkeleton /> : providers}</AuthRoomProvider>
            </InterfaceLanguageProvider>
            {/* One toaster for the whole application; anything can call toast(). */}
            <Toaster position="bottom-right" />
        </>
    );
};

export default AppWrapper;
