import React, { createContext, useContext, useState, useEffect, useMemo, useCallback, ReactNode } from 'react';
import { useAuth } from './AuthContext';
import { useLocation, useNavigate } from 'react-router-dom';


interface FavContextType {
    fav: boolean;
    favEnabled: boolean;
    setFavEnabled: (favEnabled: boolean) => void;
    setSnackbarMessage: (message: string) => void;
    snackbarMessage: string;
}

const FavContext = createContext<FavContextType | undefined>(undefined);

interface FavProviderProps {
    children: ReactNode;
}

export const FavProvider: React.FC<FavProviderProps> = ({ children }) => {
    const { user, setResetFavCallback } = useAuth();
    const [favEnabled, setFavEnabled] = useState(user?.have_favs ?? false);
    const [snackbarMessage, setSnackbarMessage] = useState('');
    const navigate = useNavigate();
    const location = useLocation();
    const isFavoritePage = location.pathname.includes('/books/favorite');

    // Регистрируем функцию сброса избранного в AuthContext
    // Функция проверяет ТЕКУЩИЙ pathname в момент вызова, а не в момент регистрации
    useEffect(() => {
        const resetFav = () => {
            const currentPath = window.location.pathname;
            if (currentPath.includes('/books/favorite')) {
                navigate('/books/page/1');
            }
        };
        // Используем стрелочную функцию, чтобы избежать проблем с React state updater
        setResetFavCallback(() => resetFav);
    }, [navigate, setResetFavCallback]);

    useEffect(() => {
        setFavEnabled(user?.have_favs ?? false);
    }, [user?.have_favs]);

    const memoizedSetFavEnabled = useCallback((favEnabled: boolean) => setFavEnabled(favEnabled), []);
    const memoizedSetSnackbarMessage = useCallback((message: string) => setSnackbarMessage(message), []);

    const contextValue = useMemo(() => ({
        fav: isFavoritePage,
        favEnabled,
        setFavEnabled: memoizedSetFavEnabled,
        setSnackbarMessage: memoizedSetSnackbarMessage,
        snackbarMessage
    }), [isFavoritePage, favEnabled, memoizedSetFavEnabled, memoizedSetSnackbarMessage, snackbarMessage]);

    return (
        <FavContext.Provider value={contextValue}>
            {children}
        </FavContext.Provider>
    );
};

export const useFav = () => {
    const context = useContext(FavContext);
    if (context === undefined) {
        throw new Error('useFav must be used within a FavProvider');
    }
    return context;
};
