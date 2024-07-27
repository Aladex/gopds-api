import React, { createContext, useContext, useState, useMemo, useCallback, ReactNode } from 'react';

interface SearchBarContextType {
    selectedSearch: string;
    searchItem: string;
    languages: string[];
    setLanguages: (languages: string[]) => void;
    setSearchItem: (searchValue: string) => void;
    setSelectedSearch: (selectedSearch: string) => void;
    clearSelectedSearch: () => void;
}

const SearchBarContext = createContext<SearchBarContextType | undefined>(undefined);

export const SearchBarProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
    const [searchItem, setSearchItem] = useState('');
    const [selectedSearch, setSelectedSearch] = useState('title'); // Initial state set to 'title'
    const [languages, setLanguages] = useState<string[]>([]);

    const clearSelectedSearch = useCallback(() => setSelectedSearch('title'), []);

    const memoizedSetLanguages = useCallback((languages: string[]) => setLanguages(languages), []);
    const memoizedSetSearchItem = useCallback((searchValue: string) => setSearchItem(searchValue), []);
    const memoizedSetSelectedSearch = useCallback((selectedSearch: string) => setSelectedSearch(selectedSearch), []);

    const contextValue = useMemo(() => ({
        searchItem,
        selectedSearch,
        languages,
        setLanguages: memoizedSetLanguages,
        setSearchItem: memoizedSetSearchItem,
        setSelectedSearch: memoizedSetSelectedSearch,
        clearSelectedSearch,
    }), [searchItem, selectedSearch, languages, memoizedSetLanguages, memoizedSetSearchItem, memoizedSetSelectedSearch, clearSelectedSearch]);

    return (
        <SearchBarContext.Provider value={contextValue}>
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
