import React, {
    createContext,
    useContext,
    useState,
    useEffect,
    useMemo,
    useCallback,
    ReactNode,
} from 'react';

import * as booksApi from '@/api/books';
import { useAuth } from '@/context/AuthContext';

export type ThemeMode = 'light' | 'dark';

interface ThemeContextType {
    mode: ThemeMode;
    toggleTheme: () => void;
    setThemeMode: (mode: ThemeMode) => void;
}

const ThemeContext = createContext<ThemeContextType | undefined>(undefined);

interface ThemeProviderProps {
    children: ReactNode;
}

/**
 * ThemeProvider owns which of the two themes the interface is showing.
 *
 * It holds no colours. The palettes live in index.css, one per theme, and this
 * only says which is in force by stamping data-theme on the root element —
 * which is what both the CSS variables and Tailwind's dark: variant key off.
 *
 * The system preference is the opening guess, replaced by the reader's own
 * stored choice as soon as it arrives.
 */
export const ThemeProvider: React.FC<ThemeProviderProps> = ({ children }) => {
    const { isLoaded, isAuthenticated } = useAuth();

    const getInitialMode = (): ThemeMode =>
        window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';

    const [mode, setMode] = useState<ThemeMode>(getInitialMode);

    useEffect(() => {
        if (!isLoaded || !isAuthenticated) {
            return;
        }

        const loadTheme = async () => {
            try {
                const { theme } = await booksApi.getThemePreference();
                if (theme === 'light' || theme === 'dark') {
                    setMode(theme);
                }
            } catch {
                // Ignore errors and keep current mode
            }
        };

        loadTheme();
    }, [isLoaded, isAuthenticated]);

    useEffect(() => {
        document.documentElement.setAttribute('data-theme', mode);
        // colour-scheme tells the browser which way to paint what this
        // application does not: scrollbars, the controls it still leaves
        // native, and the space beyond the end of the page.
        document.documentElement.style.colorScheme = mode;
    }, [mode]);

    const persistTheme = useCallback(
        async (newMode: ThemeMode) => {
            if (!isAuthenticated) {
                return;
            }
            try {
                await booksApi.setThemePreference(newMode);
            } catch {
                // Ignore errors and keep current mode
            }
        },
        [isAuthenticated],
    );

    const toggleTheme = useCallback(() => {
        setMode((prevMode) => {
            const nextMode = prevMode === 'light' ? 'dark' : 'light';
            void persistTheme(nextMode);
            return nextMode;
        });
    }, [persistTheme]);

    const setThemeMode = useCallback(
        (newMode: ThemeMode) => {
            setMode(newMode);
            void persistTheme(newMode);
        },
        [persistTheme],
    );

    const contextValue = useMemo(
        () => ({ mode, toggleTheme, setThemeMode }),
        [mode, toggleTheme, setThemeMode],
    );

    return <ThemeContext.Provider value={contextValue}>{children}</ThemeContext.Provider>;
};

export const useTheme = () => {
    const context = useContext(ThemeContext);
    if (context === undefined) {
        throw new Error('useTheme must be used within a ThemeProvider');
    }
    return context;
};
