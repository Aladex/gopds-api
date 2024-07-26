import React, {createContext, useContext, useState, ReactNode, useEffect} from 'react';
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

    return (
        <FavContext.Provider value={{ fav, favEnabled, setFav, setFavEnabled }}>
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