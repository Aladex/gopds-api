import React, { createContext, useContext, useState, useMemo, useCallback, useEffect, ReactNode } from 'react';
import { fetchWithAuth } from '../api/config'; // Предполагаем, что у вас есть функция для авторизованных запросов

interface SearchBarContextType {
    selectedSearch: string;
    searchItem: string;
    languages: string[];
    setLanguages: (languages: string[]) => void;
    setSearchItem: (searchValue: string) => void;
    setSelectedSearch: (selectedSearch: string) => void;
    clearSelectedSearch: () => void;
    setLanguage: (language: string) => void;
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

    const setLanguage = useCallback((language: string) => {
        fetchWithAuth.post('/books/change-me', { books_lang: language })
            .then(response => {
                if (response.status === 200) {
                    console.log('Language updated successfully');
                } else {
                    console.error('Failed to update language');
                }
            })
            .catch(error => {
                console.error('Error updating language', error);
            });
    }, []);

    useEffect(() => {
        const fetchLanguages = async () => {
            try {
                const response = await fetchWithAuth.get('/books/langs');
                if (response.status === 200) {
                    const data = response.data;
                    const languageList = data.langs.map((item: { language: string }) => item.language);
                    setLanguages(languageList);
                } else {
                    console.error('Failed to fetch languages');
                }
            } catch (error) {
                console.error('Error fetching languages', error);
            }
        };

        fetchLanguages();
    }, []);

    const contextValue = useMemo(() => ({
        searchItem,
        selectedSearch,
        languages,
        setLanguages: memoizedSetLanguages,
        setSearchItem: memoizedSetSearchItem,
        setSelectedSearch: memoizedSetSelectedSearch,
        clearSelectedSearch,
        setLanguage,
    }), [searchItem, selectedSearch, languages, memoizedSetLanguages, memoizedSetSearchItem, memoizedSetSelectedSearch, clearSelectedSearch, setLanguage]);

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
