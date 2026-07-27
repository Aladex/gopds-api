import React, { createContext, useContext, useState, useEffect, useMemo, useCallback, ReactNode } from 'react';
import { PaletteMode } from '@mui/material';
import { ThemeProvider as MuiThemeProvider } from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import { createAppTheme } from '@/shared/theme';
import * as booksApi from '@/api/books';
import { useAuth } from '@/context/AuthContext';

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

        // Status colours, so components outside MUI can reach the same palette
        // instead of hard-coding their own and drifting from it.
        root.style.setProperty('--app-error-main', theme.palette.error.main);
        root.style.setProperty('--app-error-contrast', theme.palette.error.contrastText);
        root.style.setProperty('--app-warning-main', theme.palette.warning.main);
        root.style.setProperty('--app-info-main', theme.palette.info.main);

        // Tailwind's dark: variant is bound to this attribute in index.css.
        // Without it the variant falls back to the operating system's preference,
        // which says nothing about the theme this application is actually showing
        // — a reader on a light desktop who chose the dark theme would get the
        // light halves of every shadcn component.
        root.setAttribute('data-theme', theme.palette.mode);
    }, [theme]);

    const persistTheme = useCallback(async (newMode: PaletteMode) => {
        if (!isAuthenticated) {
            return;
        }
        try {
            await booksApi.setThemePreference(newMode);
        } catch {
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
