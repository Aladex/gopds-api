import React, { createContext, useContext, useState, useCallback, useEffect, useMemo, ReactNode } from 'react';
import { fetchWithAuth } from '../api/config';
import { useNavigate } from 'react-router-dom';

interface User {
    username: string;
    first_name: string;
    last_name: string;
    is_superuser: boolean;
    books_lang?: string;
    have_favs?: boolean;
}

interface AuthContextType {
    isAuthenticated: boolean;
    user: User | null;
    isLoaded: boolean;
    setUser: (user: User | null) => void;
    updateUser: (userData: User) => void;
    login: () => void;
    logout: () => void;
    updateLang: (language: string) => void;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

interface AuthProviderProps {
    children: ReactNode;
}

export const AuthProvider: React.FC<AuthProviderProps> = ({ children }) => {
    const [user, setUser] = useState<User | null>(null);
    const [isLoaded, setIsLoaded] = useState<boolean>(false);
    const navigate = useNavigate();
    const isAuthenticated = !!user;

    const updateLang = useCallback(async (language: string) => {
        if (user) {
            try {
                const response = await fetchWithAuth.post('/books/change-me', { ...user, books_lang: language });
                if (response.status === 200) {
                    setUser((prevUser) => prevUser ? { ...prevUser, books_lang: response.data.books_lang } : null);
                } else {
                    console.error('Failed to update language');
                }
            } catch (error) {
                console.error('Error updating language', error);
            }
        }
    }, [user]);

    const login = useCallback(() => {
        fetchWithAuth.get('/books/self-user')
            .then((response) => {
                setUser(response.data);
            })
            .catch((error) => {
                if (error.response && error.response.status === 401) {
                    navigate('/login');
                } else {
                    console.error('Error fetching user data', error);
                    setUser(null);
                }
            })
            .finally(() => {
                setIsLoaded(true);
            });
    }, [navigate]);

    const logout = useCallback(() => {
        fetchWithAuth.get('/logout')
            .then(() => {
                setUser(null);
                window.location.href = '/login';
            })
            .catch((error) => {
                console.error('Error logging out', error);
            });
    }, []);

    const updateUser = useCallback((userData: User) => {
        setUser(userData);
    }, []);

    useEffect(() => {
        login();
    }, [login]);

    const contextValue = useMemo(() => ({
        isAuthenticated,
        user,
        isLoaded,
        setUser,
        updateLang,
        updateUser,
        login,
        logout,
    }), [isAuthenticated, user, isLoaded, updateLang, updateUser, login, logout]);

    return (
        <AuthContext.Provider value={contextValue}>
            {isLoaded && children}
        </AuthContext.Provider>
    );
};

export const useAuth = () => {
    const context = useContext(AuthContext);
    if (context === undefined) {
        throw new Error('useAuth must be used within an AuthProvider');
    }
    return context;
};
