import React, { createContext, useContext, useState, ReactNode } from 'react';

interface SearchBarContextType {
    selectedSearch: string;
    searchItem: string;
    setSearchItem: (searchValue: string) => void;
    setSelectedSearch: (selectedSearch: string) => void;
    clearSearchValue: () => void;
    clearSelectedSearch: () => void;
}


const SearchBarContext = createContext<SearchBarContextType | undefined>(undefined);

export const SearchBarProvider: React.FC<{children: ReactNode}> = ({ children }) => {
    const [searchItem, setSearchItem] = useState('');
    const [selectedSearch, setSelectedSearch] = useState('title'); // Initial state set to 'title'

    const clearSearchValue = () => setSearchItem('');
    const clearSelectedSearch = () => setSelectedSearch('title'); // Reset to 'title' instead of ''

    return (
        <SearchBarContext.Provider value={{
            searchItem,
            selectedSearch,
            setSearchItem,
            setSelectedSearch,
            clearSearchValue,
            clearSelectedSearch,
        }}>
            {children}
        </SearchBarContext.Provider>
    );
};

export const useSearchBar = (): SearchBarContextType => {
    const context = useContext(SearchBarContext);
    if (!context) {
        throw new Error('useSearchBar must be used within a SearchBarProvider');
    }
    return context;
};