import React, { createContext, useContext, useState, useMemo, useCallback, ReactNode } from 'react';

interface AuthorContextType {
    authorId: string;
    authorBook: string;
    setAuthorId: (authorId: string) => void;
    setAuthorBook: (authorBook: string) => void;
    clearAuthorId: () => void;
    clearAuthorBook: () => void;
}

const AuthorContext = createContext<AuthorContextType | undefined>(undefined);

interface AuthorProviderProps {
    children: ReactNode;
}

export const AuthorProvider: React.FC<AuthorProviderProps> = ({ children }) => {
    const [authorId, setAuthorId] = useState('');
    const [authorBook, setAuthorBook] = useState('');

    const clearAuthorId = useCallback(() => setAuthorId(''), []);
    const clearAuthorBook = useCallback(() => setAuthorBook(''), []);

    const memoizedSetAuthorId = useCallback((id: string) => setAuthorId(id), []);
    const memoizedSetAuthorBook = useCallback((book: string) => setAuthorBook(book), []);

    const contextValue = useMemo(() => ({
        authorId,
        authorBook,
        setAuthorId: memoizedSetAuthorId,
        setAuthorBook: memoizedSetAuthorBook,
        clearAuthorId,
        clearAuthorBook,
    }), [authorId, authorBook, memoizedSetAuthorId, memoizedSetAuthorBook, clearAuthorId, clearAuthorBook]);

    return (
        <AuthorContext.Provider value={contextValue}>
            {children}
        </AuthorContext.Provider>
    );
};

export const useAuthor = (): AuthorContextType => {
    const context = useContext(AuthorContext);
    if (!context) {
        throw new Error('useAuthor must be used within an AuthorProvider');
    }
    return context;
};
