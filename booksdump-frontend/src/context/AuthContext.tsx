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
    resetFavCallback?: () => void;  // Добавляем колбэк для сброса избранного
    setResetFavCallback: (callback: () => void) => void;
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
    const [resetFavCallback, setResetFavCallback] = useState<() => void>(() => () => {});
    const navigate = useNavigate();

    // Мемоизируем isAuthenticated для предотвращения ненужных перерендеров
    const isAuthenticated = useMemo(() => !!user, [user]);

    // Обертка для setUser с логированием
    const setUser = useCallback((newUser: User | null) => {
        setUserState(newUser);
    }, []);

    const getCsrfToken = useCallback(async () => {
        const cookieToken = document.cookie
            .split('; ')
            .find((row) => row.startsWith('csrf_token='))
            ?.split('=')[1];

        if (cookieToken) {
            setCsrfToken(cookieToken);
            return;
        }

        const preloadedToken = (window as Window & { __CSRF_TOKEN__?: string }).__CSRF_TOKEN__;
        if (preloadedToken) {
            setCsrfToken(preloadedToken);
            return;
        }

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

            // Check if we're on an auth page - don't redirect if we are
            const currentPath = window.location.pathname;
            const isAuthPage = currentPath.includes('/login') ||
                              currentPath.includes('/register') ||
                              currentPath.includes('/forgot-password') ||
                              currentPath.includes('/activation') ||
                              currentPath.includes('/activate') ||
                              currentPath.includes('/change-password');

            setUser(null);
            if (!isAuthPage) {
                navigate('/login');
            }
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
                    // Сбрасываем избранное перед сменой языка
                    if (resetFavCallback) {
                        resetFavCallback();
                    }

                    // Обновляем пользователя с новым языком
                    setUser({ ...user, books_lang: language });

                    // При смене языка всегда перенаправляем на обычную страницу книг
                    navigate('/books/page/1');
                } else {
                    console.error('Failed to update language');
                }
            } catch (error) {
                console.error('Error updating language', error);
            }
        }
    }, [user, navigate, setUser, resetFavCallback]);

    const logout = useCallback(async () => {
        if (isLoading) return;

        setIsLoading(true);
        try {
            await fetchWithAuth.get('/logout');
        } catch (error) {
            console.error('Error logging out', error);
        } finally {
            // Всегда сбрасываем пользователя и получаем новый CSRF токен
            setUser(null);
            setCsrfToken(null);

            // Получаем новый CSRF токен после логаута
            try {
                await getCsrfToken();
            } catch (error) {
                console.error('Error getting new CSRF token after logout', error);
            }

            setIsLoading(false);
            navigate('/login');
        }
    }, [navigate, isLoading, setUser, getCsrfToken]);

    const updateUser = useCallback((userData: User) => {
        setUser(userData);
    }, [setUser]);

    // Initialize CSRF token and user data
    useEffect(() => {
        const initializeAuth = async () => {
            // Check current path first
            const currentPath = window.location.pathname;
            const isChangePasswordPage = currentPath.includes('/change-password');

            // For change password page, only get CSRF token and skip all auth checks
            if (isChangePasswordPage) {
                setIsLoading(true);
                try {
                    await getCsrfToken();
                } finally {
                    setIsLoaded(true);
                    setIsLoading(false);
                }
                return;
            }

            setIsLoading(true);
            try {
                const response = await fetchWithAuth.get('/init');
                const initData = response.data || {};
                if (initData.csrf_token) {
                    setCsrfToken(initData.csrf_token);
                } else {
                    await getCsrfToken();
                }
                setUser(initData.user || null);
            } catch (error) {
                console.error('Error initializing auth', error);
                setUser(null);
                await getCsrfToken();
            } finally {
                setIsLoaded(true);
                setIsLoading(false);
            }
        };
        initializeAuth();
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [getCsrfToken]); // setUser is stable and login removed to avoid extra requests

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
        resetFavCallback,
        setResetFavCallback,
    }), [isAuthenticated, user, isLoaded, isLoading, csrfToken, setUser, updateLang, updateUser, login, logout, refreshToken, getCsrfToken, resetFavCallback]);

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
