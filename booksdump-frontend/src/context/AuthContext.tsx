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
    isLoading: boolean;
    csrfToken: string | null;
    setUser: (user: User | null) => void;
    updateUser: (userData: User) => void;
    login: () => void;
    logout: () => void;
    updateLang: (language: string) => void;
    refreshToken: () => Promise<boolean>;
    getCsrfToken: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

interface AuthProviderProps {
    children: ReactNode;
}

export const AuthProvider: React.FC<AuthProviderProps> = ({ children }) => {
    const [user, setUserState] = useState<User | null>(null);
    const [isLoaded, setIsLoaded] = useState<boolean>(false);
    const [isLoading, setIsLoading] = useState<boolean>(false);
    const [csrfToken, setCsrfToken] = useState<string | null>(null);
    const navigate = useNavigate();

    // Мемоизируем isAuthenticated для предотвращения ненужных перерендеров
    const isAuthenticated = useMemo(() => !!user, [user]);

    // Обертка для setUser с логированием
    const setUser = useCallback((newUser: User | null) => {
        setUserState(newUser);
    }, []);

    const getCsrfToken = useCallback(async () => {
        try {
            const response = await fetchWithAuth.get('/csrf-token');
            if (response.status === 200 && response.data.csrf_token) {
                setCsrfToken(response.data.csrf_token);
            }
        } catch (error) {
            console.error('Error fetching CSRF token', error);
        }
    }, []);

    const refreshToken = useCallback(async () => {
        try {
            const response = await fetchWithAuth.post('/refresh-token');
            if (response.status === 200) {
                return true;
            }
        } catch (error) {
            console.error('Error refreshing token', error);
            setUser(null);
            navigate('/login');
        }
        return false;
    }, [navigate, setUser]);

    const login = useCallback(() => {
        // Предотвращаем множественные вызовы login
        if (isLoading) return;

        setIsLoading(true);
        fetchWithAuth.get('/books/self-user')
            .then((response) => {
                setUser(response.data);
            })
            .catch(async (error) => {
                if (error.response && error.response.status === 401) {
                    // Try to refresh token once
                    const refreshed = await refreshToken();
                    if (refreshed) {
                        // Retry getting user data
                        try {
                            const response = await fetchWithAuth.get('/books/self-user');
                            setUser(response.data);
                        } catch (retryError) {
                            setUser(null);
                        }
                    } else {
                        setUser(null);
                    }
                } else {
                    console.error('Error fetching user data', error);
                    setUser(null);
                }
            })
            .finally(() => {
                setIsLoaded(true);
                setIsLoading(false);
            });
    }, [refreshToken, isLoading, setUser]);

    const updateLang = useCallback(async (language: string) => {
        if (user) {
            try {
                const response = await fetchWithAuth.post('/books/change-me', {
                    books_lang: language
                });
                if (response.status === 200) {
                    // Обновляем пользователя с новым языком
                    setUser({ ...user, books_lang: language });
                    // Используем navigate вместо window.location.assign для SPA навигации
                    navigate('/books/page/1');
                } else {
                    console.error('Failed to update language');
                }
            } catch (error) {
                console.error('Error updating language', error);
            }
        }
    }, [user, navigate, setUser]);

    const logout = useCallback(() => {
        if (isLoading) return;

        setIsLoading(true);
        fetchWithAuth.get('/logout')
            .then(() => {
                setUser(null);
                setCsrfToken(null);
                navigate('/login');
            })
            .catch((error) => {
                console.error('Error logging out', error);
                // Force logout even if request fails
                setUser(null);
                setCsrfToken(null);
                navigate('/login');
            })
            .finally(() => {
                setIsLoading(false);
            });
    }, [navigate, isLoading, setUser]);

    const updateUser = useCallback((userData: User) => {
        setUser(userData);
    }, [setUser]);

    // Initialize CSRF token and user data
    useEffect(() => {
        const initializeAuth = async () => {
            await getCsrfToken();

            // Check current path
            const currentPath = window.location.pathname;
            const isAuthPage = currentPath.includes('/login') ||
                              currentPath.includes('/register') ||
                              currentPath.includes('/forgot-password') ||
                              currentPath.includes('/activation') ||
                              currentPath.includes('/activate') ||
                              currentPath.includes('/change-password');

            if (!isAuthPage) {
                // For non-auth pages, try to login normally
                login();
            } else {
                // For auth pages, still check if user is already authenticated
                // but do it silently without triggering redirects
                setIsLoading(true);
                fetchWithAuth.get('/books/self-user')
                    .then((response) => {
                        setUser(response.data);
                    })
                    .catch(() => {
                        // Silent fail - user is not authenticated, which is expected on auth pages
                        setUser(null);
                    })
                    .finally(() => {
                        setIsLoaded(true);
                        setIsLoading(false);
                    });
            }
        };
        initializeAuth();
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [getCsrfToken]); // login и setUser намеренно исключены для предотвращения бесконечных циклов

    // Мемоизируем значение контекста для предотвращения ненужных перерендеров
    const contextValue = useMemo(() => ({
        isAuthenticated,
        user,
        isLoaded,
        isLoading,
        csrfToken,
        setUser,
        updateLang,
        updateUser,
        login,
        logout,
        refreshToken,
        getCsrfToken,
    }), [isAuthenticated, user, isLoaded, isLoading, csrfToken, setUser, updateLang, updateUser, login, logout, refreshToken, getCsrfToken]);

    return (
        <AuthContext.Provider value={contextValue}>
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
