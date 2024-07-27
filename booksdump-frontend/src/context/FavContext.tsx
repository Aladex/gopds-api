import React, { createContext, useContext, useState, useEffect, useMemo, useCallback, ReactNode } from 'react';
import { useAuth } from './AuthContext';
import { useNavigate } from 'react-router-dom';


interface FavContextType {
    fav: boolean;
    favEnabled: boolean;
    setFav: (fav: boolean) => void;
    setFavEnabled: (favEnabled: boolean) => void;
}

const FavContext = createContext<FavContextType | undefined>(undefined);

interface FavProviderProps {
    children: ReactNode;
}

export const FavProvider: React.FC<FavProviderProps> = ({ children }) => {
    const { user } = useAuth();
    const [fav, setFav] = useState(false);
    const [favEnabled, setFavEnabled] = useState(user?.have_favs ?? false);
    const navigate = useNavigate();

    useEffect(() => {
        setFavEnabled(user?.have_favs ?? false);
    }, [user?.have_favs]);

    useEffect(() => {
        if (fav && favEnabled) {
            navigate('/books/favorite/1');
        } else if (!fav && window.location.pathname.includes('/books/favorite')) {
            navigate('/books/page/1');
        }

    }, [fav, favEnabled, navigate]);

    const memoizedSetFav = useCallback((fav: boolean) => setFav(fav), []);
    const memoizedSetFavEnabled = useCallback((favEnabled: boolean) => setFavEnabled(favEnabled), []);

    const contextValue = useMemo(() => ({
        fav,
        favEnabled,
        setFav: memoizedSetFav,
        setFavEnabled: memoizedSetFavEnabled
    }), [fav, favEnabled, memoizedSetFav, memoizedSetFavEnabled]);

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
