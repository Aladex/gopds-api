import React, { createContext, useContext, useState, ReactNode, useCallback } from 'react';
import Cookies from 'js-cookie';

interface User {
    username: string;
    first_name: string;
    last_name: string;
    is_superuser: boolean;
    books_lang?: string;
}

interface AuthContextType {
    isAuthenticated: boolean;
    token: string | null;
    user: User | null;
    setUser: (user: User | null) => void;
    updateUser: (userData: User) => void; // Method to update user data
    login: (token: string) => void;
    logout: () => void;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

interface AuthProviderProps {
    children: ReactNode;
}

export const AuthProvider: React.FC<AuthProviderProps> = ({ children }) => {
    const [token, setTokenState] = useState<string | null>(getToken());
    const [user, setUser] = useState<User | null>(null);
    const isAuthenticated = !!token;

    const login = (token: string) => {
        setToken(token);
        setTokenState(token);
    };

    const logout = () => {
        removeToken();
        setTokenState(null);
        setUser(null); // Ensure user is set to null on logout
        window.location.href = '/login'; // Redirect to login page on logout
    };

    const updateUser = useCallback((userData: User) => {
        setUser(userData); // Update user data in context
    }, []);

    return (
        <AuthContext.Provider value={{ isAuthenticated, token, user, setUser, updateUser, login, logout }}>
            {children}
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

export const getToken = () => {
    return Cookies.get('token') || null;
};

export const setToken = (token: string) => {
    Cookies.set('token', token, { expires: 7, secure: true, sameSite: 'strict' });
};

export const removeToken = () => {
    Cookies.remove('token');
};
