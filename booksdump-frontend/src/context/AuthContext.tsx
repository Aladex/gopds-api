import React, { createContext, useContext, useState, useCallback, useEffect, ReactNode } from 'react';
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

    const logout = () => {
        fetchWithAuth.get('/logout')
            .then(() => {
                setUser(null);
                window.location.href = '/login';
            })
            .catch((error) => {
                console.error('Error logging out', error);
            });
    };

    const updateUser = useCallback((userData: User) => {
        setUser(userData);
    }, []);

    useEffect(() => {
        login();
    }, [login]);

    return (
        <AuthContext.Provider value={{ isAuthenticated, user, isLoaded, setUser, updateUser, login, logout }}>
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
