import React, { createContext, useContext, useState, ReactNode } from 'react';

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

    const clearAuthorId = () => setAuthorId('');
    const clearAuthorBook = () => setAuthorBook('');

    return (
        <AuthorContext.Provider value={{ authorId, authorBook, setAuthorId, setAuthorBook, clearAuthorId, clearAuthorBook }}>
            {children}
        </AuthorContext.Provider>
    );
};

export const useAuthor = () => {
    const context = useContext(AuthorContext);
    if (context === undefined) {
        throw new Error('useAuthor must be used within a AuthorProvider');
    }
    return context;
};