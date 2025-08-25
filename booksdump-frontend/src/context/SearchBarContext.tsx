import React, { createContext, useContext, useState, useMemo, useCallback, useEffect, ReactNode } from 'react';
import { fetchWithAuth } from '../api/config';
import { useAuth } from './AuthContext';
import { filterSupportedLanguages } from '../utils/languageUtils';

interface SearchBarContextType {
    selectedSearch: string;
    searchItem: string;
    languages: string[];
    selectedLanguage: string;
    setLanguages: (languages: string[]) => void;
    setSearchItem: (searchValue: string) => void;
    setSelectedSearch: (selectedSearch: string) => void;
    setSelectedLanguage: (language: string) => void;
    clearSelectedSearch: () => void;
}

const SearchBarContext = createContext<SearchBarContextType | undefined>(undefined);

export const SearchBarProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
    const [searchItem, setSearchItem] = useState('');
    const [selectedSearch, setSelectedSearch] = useState('title'); // Initial state set to 'title'
    const [languages, setLanguages] = useState<string[]>([]);
    const [selectedLanguage, setSelectedLanguage] = useState<string>('');
    const { user, isAuthenticated } = useAuth();

    const clearSelectedSearch = useCallback(() => setSelectedSearch('title'), []);

    const memoizedSetLanguages = useCallback((languages: string[]) => setLanguages(languages), []);
    const memoizedSetSearchItem = useCallback((searchValue: string) => setSearchItem(searchValue), []);
    const memoizedSetSelectedSearch = useCallback((selectedSearch: string) => setSelectedSearch(selectedSearch), []);
    const memoizedSetSelectedLanguage = useCallback((language: string) => setSelectedLanguage(language), []);

    useEffect(() => {
        setSelectedLanguage(user?.books_lang ?? '');
    }, [user?.books_lang]);

    useEffect(() => {
        if (isAuthenticated) {
            const fetchLanguages = async () => {
                try {
                    const response = await fetchWithAuth.get('/books/langs');
                    if (response.status === 200) {
                        const data = response.data;
                        const languageList = data.langs.map((item: { language: string }) => item.language);
                        // Filter only supported languages
                        const supportedLanguages = filterSupportedLanguages(languageList);
                        setLanguages(supportedLanguages);
                    } else {
                        console.error('Failed to fetch languages');
                    }
                } catch (error) {
                    console.error('Error fetching languages', error);
                }
            };

            fetchLanguages();
        }
    }, [isAuthenticated]);

    const contextValue = useMemo(() => ({
        searchItem,
        selectedSearch,
        languages,
        selectedLanguage,
        setLanguages: memoizedSetLanguages,
        setSearchItem: memoizedSetSearchItem,
        setSelectedSearch: memoizedSetSelectedSearch,
        setSelectedLanguage: memoizedSetSelectedLanguage,
        clearSelectedSearch,
    }), [searchItem, selectedSearch, languages, selectedLanguage, memoizedSetLanguages, memoizedSetSearchItem, memoizedSetSelectedSearch, memoizedSetSelectedLanguage, clearSelectedSearch]);

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
