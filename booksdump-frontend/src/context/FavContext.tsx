import React, { createContext, useContext, useState, ReactNode } from 'react';

interface FavContextType {
    fav: boolean;
    setFav: (fav: boolean) => void;
}

const FavContext = createContext<FavContextType | undefined>(undefined);

interface FavProviderProps {
    children: ReactNode;
}

export const FavProvider: React.FC<FavProviderProps> = ({ children }) => {
    const [fav, setFav] = useState(false);

    return (
        <FavContext.Provider value={{ fav, setFav }}>
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