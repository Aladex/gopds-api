import React, {
    createContext,
    useContext,
    useState,
    useCallback,
    useEffect,
    useMemo,
    ReactNode,
} from 'react';
import * as authApi from '@/api/auth';
import type { User } from '@/api/auth';
import { isApiError } from '@/api/errors';
import { useNavigate } from 'react-router';

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
    resetFavCallback?: () => void; // Callback that resets favorites.
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

    const isAuthenticated = !!user;

    // Keep the setter identity stable for context consumers.
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
            const { csrf_token: token } = await authApi.getCsrfToken();
            if (token) {
                setCsrfToken(token);
            }
        } catch (error) {
            console.error('Error fetching CSRF token', error);
        }
    }, []);

    const refreshToken = useCallback(async () => {
        try {
            await authApi.refreshSession();
            return true;
        } catch (error) {
            console.error('Error refreshing token', error);

            // Check if we're on an auth page - don't redirect if we are
            const currentPath = window.location.pathname;
            const isAuthPage =
                currentPath.includes('/login') ||
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
        // Prevent concurrent login calls.
        if (isLoading) return;

        setIsLoading(true);
        // The transport already refreshes once and replays on 401, so a 401
        // reaching here means the session is genuinely gone.
        authApi
            .getCurrentUser()
            .then((currentUser) => {
                setUser(currentUser);
            })
            .catch((error: unknown) => {
                if (!isApiError(error) || !error.isUnauthorized) {
                    console.error('Error fetching user data', error);
                }
                setUser(null);
            })
            .finally(() => {
                setIsLoaded(true);
                setIsLoading(false);
            });
    }, [refreshToken, isLoading, setUser]);

    const updateLang = useCallback(
        async (language: string) => {
            if (user) {
                try {
                    await authApi.updateCurrentUser({ books_lang: language });

                    // Reset favorites before changing the catalogue language.
                    if (resetFavCallback) {
                        resetFavCallback();
                    }

                    // Update the user with the new catalogue language.
                    setUser({ ...user, books_lang: language });

                    // Return to the main book list after changing language.
                    navigate('/books/page/1');
                } catch (error) {
                    console.error('Error updating language', error);
                }
            }
        },
        [user, navigate, setUser, resetFavCallback],
    );

    const logout = useCallback(async () => {
        if (isLoading) return;

        setIsLoading(true);
        try {
            await authApi.logout();
        } catch (error) {
            console.error('Error logging out', error);
        } finally {
            // Always clear the user and request a new CSRF token.
            setUser(null);
            setCsrfToken(null);

            // Request a new CSRF token after logout.
            try {
                await getCsrfToken();
            } catch (error) {
                console.error('Error getting new CSRF token after logout', error);
            }

            setIsLoading(false);
            navigate('/login');
        }
    }, [navigate, isLoading, setUser, getCsrfToken]);

    const updateUser = useCallback(
        (userData: User) => {
            setUser(userData);
        },
        [setUser],
    );

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
                const initData = (await authApi.getInit()) ?? {};
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

    // Memoize the context value to avoid unnecessary rerenders.
    const contextValue = useMemo(
        () => ({
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
        }),
        [
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
        ],
    );

    return <AuthContext.Provider value={contextValue}>{children}</AuthContext.Provider>;
};

export const useAuth = () => {
    const context = useContext(AuthContext);
    if (context === undefined) {
        throw new Error('useAuth must be used within an AuthProvider');
    }
    return context;
};
