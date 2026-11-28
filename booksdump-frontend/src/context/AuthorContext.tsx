import React, {
    createContext,
    useContext,
    useState,
    useMemo,
    useCallback,
    useRef,
    ReactNode,
} from 'react';

import type { NamedAuthor } from '@/shared/lib/authorTitle';

interface AuthorContextType {
    authorId: string;
    /**
     * The author's name, for saying whose books are being searched: the full
     * and the short name and a namesake's label, as the server sends them. It
     * is only ever a cache: whoever knows the name puts it here — a book card
     * knows it from the link that was clicked — and the search chip fetches
     * it by id when nobody did, which is what happens on a reload or a pasted
     * URL. Null until then.
     */
    authorName: NamedAuthor | null;
    setAuthorId: (authorId: string) => void;
    setAuthorName: (authorName: NamedAuthor) => void;
    clearAuthorId: () => void;
}

const AuthorContext = createContext<AuthorContextType | undefined>(undefined);

interface AuthorProviderProps {
    children: ReactNode;
}

export const AuthorProvider: React.FC<AuthorProviderProps> = ({ children }) => {
    const [authorId, setAuthorId] = useState('');
    const [authorName, setAuthorName] = useState<NamedAuthor | null>(null);

    // The id is set on every list load, usually to the value it already holds,
    // so the comparison is against a ref rather than a state updater — dropping
    // the name from inside an updater would run twice under StrictMode.
    const knownId = useRef('');

    // The name belongs to the id, so dropping one drops the other. Keeping a
    // stale name would put the wrong author's name on the next author's scope.
    const clearAuthorId = useCallback(() => {
        knownId.current = '';
        setAuthorId('');
        setAuthorName(null);
    }, []);

    const memoizedSetAuthorId = useCallback((id: string) => {
        if (knownId.current !== id) {
            knownId.current = id;
            setAuthorName(null);
        }
        setAuthorId(id);
    }, []);
    const memoizedSetAuthorName = useCallback((name: NamedAuthor) => setAuthorName(name), []);

    const contextValue = useMemo(
        () => ({
            authorId,
            authorName,
            setAuthorId: memoizedSetAuthorId,
            setAuthorName: memoizedSetAuthorName,
            clearAuthorId,
        }),
        [authorId, authorName, memoizedSetAuthorId, memoizedSetAuthorName, clearAuthorId],
    );

    return <AuthorContext.Provider value={contextValue}>{children}</AuthorContext.Provider>;
};

export const useAuthor = (): AuthorContextType => {
    const context = useContext(AuthorContext);
    if (!context) {
        throw new Error('useAuthor must be used within an AuthorProvider');
    }
    return context;
};
