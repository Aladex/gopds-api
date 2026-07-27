import React, { createContext, useContext, useEffect, useMemo, ReactNode } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';

import { useAuth } from '@/context/AuthContext';

interface FavContextType {
    /** Whether the reader is looking at their favourites right now. */
    fav: boolean;
    /** Whether they have any favourites, so the filter is worth offering. */
    favEnabled: boolean;
}

const FavContext = createContext<FavContextType | undefined>(undefined);

interface FavProviderProps {
    children: ReactNode;
}

/**
 * FavProvider answers two questions about favourites, both of which are read
 * from somewhere else rather than stored here: the route says whether the reader
 * is on the favourites page, and the reader's own record says whether they have
 * any.
 *
 * favEnabled used to be state, seeded from the user and then written to from two
 * places — an effect mirroring the user, and the favourite toggle. Keeping a copy
 * of a fact the user record already holds meant the two could disagree, and they
 * did: the toggle refreshed the copy and left the original stale.
 */
export const FavProvider: React.FC<FavProviderProps> = ({ children }) => {
    const { user, setResetFavCallback } = useAuth();
    const navigate = useNavigate();
    const location = useLocation();

    const isFavoritePage = location.pathname.includes('/books/favorite');
    const favEnabled = user?.have_favs ?? false;

    // Registering the callback is a genuine side effect: it hands a function to
    // another context. It reads the path at call time rather than at
    // registration, so a stale closure cannot send the reader somewhere they
    // were several navigations ago.
    useEffect(() => {
        const resetFav = () => {
            if (window.location.pathname.includes('/books/favorite')) {
                navigate('/books/page/1');
            }
        };
        setResetFavCallback(() => resetFav);
    }, [navigate, setResetFavCallback]);

    const contextValue = useMemo(
        () => ({ fav: isFavoritePage, favEnabled }),
        [isFavoritePage, favEnabled],
    );

    return <FavContext.Provider value={contextValue}>{children}</FavContext.Provider>;
};

export const useFav = () => {
    const context = useContext(FavContext);
    if (context === undefined) {
        throw new Error('useFav must be used within a FavProvider');
    }
    return context;
};
