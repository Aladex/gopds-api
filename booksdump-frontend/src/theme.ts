// src/theme.ts
import { createTheme, ThemeOptions, PaletteMode } from '@mui/material/styles';

// Define color palettes for both light and dark modes
const getDesignTokens = (mode: PaletteMode): ThemeOptions => ({
    palette: {
        mode,
        ...(mode === 'light'
            ? {
                // Light mode colors
                primary: {
                    main: '#ffffff',
                    contrastText: '#2f2f2f',
                },
                secondary: {
                    main: '#2f2f2f',
                    contrastText: '#ffffff',
                },
                background: {
                    default: '#b3b3b3',
                    paper: '#ffffff',
                },
                text: {
                    primary: '#000000',
                    secondary: 'rgba(0, 0, 0, 0.6)',
                },
                divider: 'rgba(0, 0, 0, 0.12)',
            }
            : {
                // Dark mode colors
                primary: {
                    main: '#2f2f2f',
                    contrastText: '#ffffff',
                },
                secondary: {
                    main: '#5a5a5a',
                    contrastText: '#ffffff',
                },
                background: {
                    default: '#121212',
                    paper: '#1e1e1e',
                },
                text: {
                    primary: '#ffffff',
                    secondary: 'rgba(255, 255, 255, 0.7)',
                },
                divider: 'rgba(255, 255, 255, 0.12)',
            }),
        // Colors that remain consistent in both themes
        error: {
            main: '#FF5252',
        },
        warning: {
            main: '#FFC107',
        },
        info: {
            main: '#2196F3',
        },
        success: {
            main: '#4CAF50',
        },
    },
    components: {
        MuiAppBar: {
            styleOverrides: {
                root: {
                    backgroundColor: '#2f2f2f',
                    color: '#ffffff',
                    backgroundImage: 'none',
                },
            },
        },
        MuiCard: {
            styleOverrides: {
                root: {
                    backgroundImage: 'none',
                },
            },
        },
    },
});

export const createAppTheme = (mode: PaletteMode) => createTheme(getDesignTokens(mode));

// For backwards compatibility, export default theme in light mode
const theme = createAppTheme('light');
export default theme;
