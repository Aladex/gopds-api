import React, { createContext, useContext, useState, useEffect, useMemo, useCallback, ReactNode } from 'react';
import { PaletteMode } from '@mui/material';
import { ThemeProvider as MuiThemeProvider } from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import { createAppTheme } from '../theme';
import { fetchWithAuth } from '../api/config';
import { useAuth } from './AuthContext';

interface ThemeContextType {
    mode: PaletteMode;
    toggleTheme: () => void;
    setThemeMode: (mode: PaletteMode) => void;
}

const ThemeContext = createContext<ThemeContextType | undefined>(undefined);

interface ThemeProviderProps {
    children: ReactNode;
}

export const ThemeProvider: React.FC<ThemeProviderProps> = ({ children }) => {
    const { isLoaded, isAuthenticated } = useAuth();

    // Initialize from system preference
    const getInitialMode = (): PaletteMode => {
        // Check system preference
        if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) {
            return 'dark';
        }

        return 'light';
    };

    const [mode, setMode] = useState<PaletteMode>(getInitialMode);

    // Create theme based on mode
    const theme = useMemo(() => createAppTheme(mode), [mode]);

    useEffect(() => {
        if (!isLoaded || !isAuthenticated) {
            return;
        }

        const loadTheme = async () => {
            try {
                const response = await fetchWithAuth.get('/books/theme');
                const theme = response.data?.theme;
                if (theme === 'light' || theme === 'dark') {
                    setMode(theme);
                }
            } catch (error) {
                // Ignore errors and keep current mode
            }
        };

        loadTheme();
    }, [isLoaded, isAuthenticated]);

    useEffect(() => {
        const root = document.documentElement;
        const secondaryDark = theme.palette.secondary.dark ?? theme.palette.secondary.main;

        root.style.setProperty('--app-bg-default', theme.palette.background.default);
        root.style.setProperty('--app-bg-paper', theme.palette.background.paper);
        root.style.setProperty('--app-bg-muted', theme.palette.action.selected);
        root.style.setProperty('--app-text-primary', theme.palette.text.primary);
        root.style.setProperty('--app-text-secondary', theme.palette.text.secondary);
        root.style.setProperty('--app-divider', theme.palette.divider);
        root.style.setProperty('--app-action-hover', theme.palette.action.hover);
        root.style.setProperty('--app-secondary-main', theme.palette.secondary.main);
        root.style.setProperty('--app-secondary-contrast', theme.palette.secondary.contrastText);
        root.style.setProperty('--app-secondary-dark', secondaryDark);
    }, [theme]);

    const persistTheme = useCallback(async (newMode: PaletteMode) => {
        if (!isAuthenticated) {
            return;
        }
        try {
            await fetchWithAuth.post('/books/theme', { theme: newMode });
        } catch (error) {
            // Ignore errors and keep current mode
        }
    }, [isAuthenticated]);

    const toggleTheme = useCallback(() => {
        setMode((prevMode) => {
            const nextMode = prevMode === 'light' ? 'dark' : 'light';
            void persistTheme(nextMode);
            return nextMode;
        });
    }, [persistTheme]);

    const setThemeMode = useCallback((newMode: PaletteMode) => {
        setMode(newMode);
        void persistTheme(newMode);
    }, [persistTheme]);

    const contextValue = useMemo(
        () => ({
            mode,
            toggleTheme,
            setThemeMode,
        }),
        [mode, toggleTheme, setThemeMode]
    );

    return (
        <ThemeContext.Provider value={contextValue}>
            <MuiThemeProvider theme={theme}>
                <CssBaseline />
                {children}
            </MuiThemeProvider>
        </ThemeContext.Provider>
    );
};

export const useTheme = () => {
    const context = useContext(ThemeContext);
    if (context === undefined) {
        throw new Error('useTheme must be used within a ThemeProvider');
    }
    return context;
};
