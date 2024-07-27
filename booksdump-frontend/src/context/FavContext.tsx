import React, { createContext, useContext, useState, useEffect, useMemo, useCallback, ReactNode } from 'react';
import { useAuth } from './AuthContext';

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

    useEffect(() => {
        setFavEnabled(user?.have_favs ?? false);
    }, [user?.have_favs]);

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
