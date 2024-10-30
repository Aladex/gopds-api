import React, { useState } from 'react';
import { ThemeProvider } from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import theme from './theme';
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

const App: React.FC = () => {
    useAuthWebSocket("/api/books/ws");
    return (
        <ThemeProvider theme={theme}>
            <CssBaseline />
                <Routes>
                    <Route path="/" element={<Navigate to="/books/page/1" />} />
                    {publicRoutes}
                    {privateRoutes}
                    {adminRoutes}
                    {notFoundRoutes}
                </Routes>

        </ThemeProvider>
    );
};

const AppWrapper: React.FC = () => {
    const [isLanguageLoaded, setIsLanguageLoaded] = useState(false);
    const { isLoaded } = useAuth();
    return (
        <>
            <LanguageInitializer onLanguageLoaded={() => setIsLanguageLoaded(true)} />
            {isLoaded && isLanguageLoaded && (
                <FavProvider>
                    <AuthorProvider>
                        <SearchBarProvider>
                            <BookConversionProvider>
                               <App />
                            </BookConversionProvider>
                        </SearchBarProvider>
                    </AuthorProvider>
                </FavProvider>
            )}
        </>
    );
};

export default AppWrapper;
