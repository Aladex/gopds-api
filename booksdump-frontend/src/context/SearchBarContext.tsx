import React, {
    createContext,
    useContext,
    useState,
    useMemo,
    useCallback,
    useEffect,
    ReactNode,
} from 'react';
import * as booksApi from '@/api/books';
import { useAuth } from '@/context/AuthContext';
import { filterSupportedLanguages } from '@/shared/lib/languageUtils';

interface SearchBarContextType {
    selectedSearch: string;
    searchItem: string;
    languages: string[];
    /**
     * Whether the language list has been answered for at all.
     *
     * An empty list means two different things — not fetched yet, and a
     * library with no books in it — and anything deciding what to do about a
     * reader with no language set has to tell them apart.
     */
    languagesLoaded: boolean;
    selectedLanguage: string;
    setLanguages: (languages: string[]) => void;
    setSearchItem: (searchValue: string) => void;
    setSelectedSearch: (selectedSearch: string) => void;
    setSelectedLanguage: (language: string) => void;
    clearSelectedSearch: () => void;
    /**
     * What the list the reader is standing in is called — a genre, a series,
     * a collection. The name is not in the route, which carries only an id,
     * and no public endpoint resolves one; the books on the page carry it, so
     * the list publishes it here and the search panel reads it.
     */
    scopeName: string;
    setScopeName: (name: string) => void;
}

const SearchBarContext = createContext<SearchBarContextType | undefined>(undefined);

export const SearchBarProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
    const [searchItem, setSearchItem] = useState('');
    const [selectedSearch, setSelectedSearch] = useState('title'); // Initial state set to 'title'
    const [languages, setLanguages] = useState<string[]>([]);
    const [languagesLoaded, setLanguagesLoaded] = useState(false);
    const [selectedLanguage, setSelectedLanguage] = useState<string>('');
    const [scopeName, setScopeName] = useState('');
    const { user, isAuthenticated } = useAuth();

    const clearSelectedSearch = useCallback(() => setSelectedSearch('title'), []);

    const memoizedSetLanguages = useCallback((languages: string[]) => setLanguages(languages), []);
    const memoizedSetSearchItem = useCallback(
        (searchValue: string) => setSearchItem(searchValue),
        [],
    );
    const memoizedSetSelectedSearch = useCallback(
        (selectedSearch: string) => setSelectedSearch(selectedSearch),
        [],
    );
    const memoizedSetSelectedLanguage = useCallback(
        (language: string) => setSelectedLanguage(language),
        [],
    );
    const memoizedSetScopeName = useCallback((name: string) => setScopeName(name), []);

    useEffect(() => {
        setSelectedLanguage(user?.books_lang ?? '');
    }, [user?.books_lang]);

    useEffect(() => {
        if (isAuthenticated) {
            const fetchLanguages = async () => {
                try {
                    const { langs } = await booksApi.listLanguages();
                    const languageList = langs.map((item) => item.lang);
                    // Filter only supported languages
                    setLanguages(filterSupportedLanguages(languageList));
                    setLanguagesLoaded(true);
                } catch (error) {
                    console.error('Error fetching languages', error);
                }
            };

            fetchLanguages();
        }
    }, [isAuthenticated]);

    const contextValue = useMemo(
        () => ({
            searchItem,
            selectedSearch,
            languages,
            languagesLoaded,
            selectedLanguage,
            setLanguages: memoizedSetLanguages,
            setSearchItem: memoizedSetSearchItem,
            setSelectedSearch: memoizedSetSelectedSearch,
            setSelectedLanguage: memoizedSetSelectedLanguage,
            clearSelectedSearch,
            scopeName,
            setScopeName: memoizedSetScopeName,
        }),
        [
            searchItem,
            selectedSearch,
            languages,
            languagesLoaded,
            selectedLanguage,
            memoizedSetLanguages,
            memoizedSetSearchItem,
            memoizedSetSelectedSearch,
            memoizedSetSelectedLanguage,
            clearSelectedSearch,
            scopeName,
            memoizedSetScopeName,
        ],
    );

    return <SearchBarContext.Provider value={contextValue}>{children}</SearchBarContext.Provider>;
};

export const useSearchBar = (): SearchBarContextType => {
    const context = useContext(SearchBarContext);
    if (!context) {
        throw new Error('useSearchBar must be used within a SearchBarProvider');
    }
    return context;
};
