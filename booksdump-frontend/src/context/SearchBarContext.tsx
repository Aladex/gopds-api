import React, {createContext, useContext, useState, ReactNode, useEffect} from 'react';
import { useAuth } from './AuthContext';
import { fetchWithAuth } from "../api/config";

interface SearchBarContextType {
    selectedSearch: string;
    searchItem: string;
    languagesList: string[];
    setLanguages: (languagesList: string[]) => void;
    setSearchItem: (searchValue: string) => void;
    setSelectedSearch: (selectedSearch: string) => void;
    clearSelectedSearch: () => void;
    selectedLanguage: string;
    setSelectedLanguage: (selectedLanguage: string) => void;
}


const SearchBarContext = createContext<SearchBarContextType | undefined>(undefined);

export const SearchBarProvider: React.FC<{children: ReactNode}> = ({ children }) => {
    const [searchItem, setSearchItem] = useState('');
    const [selectedSearch, setSelectedSearch] = useState('title'); // Initial state set to 'title'
    const [languagesList, setLanguages] = useState<string[]>([]);
    const [selectedLanguage, setSelectedLanguage] = useState('');
    const { user } = useAuth();


    const clearSelectedSearch = () => setSelectedSearch('title');

    useEffect(() => {
        const response = fetchWithAuth.get('/books/langs');
        response.then((response) => {
            // {langs: {"language": "Language 1", count: 1}, {"language": "Language 2", count: 2}}
            const languages = response.data.langs;
            // Set the languagesList to the array of languages
            setLanguages(languages.map((lang: {language: string}) => lang.language));
        }).catch((error) => {
            console.error('Error fetching languages', error);
        });
    }, []);

    useEffect(() => {
        setSelectedLanguage(user?.books_lang ?? '');
    }, [user]);


    return (
        <SearchBarContext.Provider value={{
            searchItem,
            selectedSearch,
            selectedLanguage,
            languagesList,
            setLanguages,
            setSelectedLanguage,
            setSearchItem,
            setSelectedSearch,
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