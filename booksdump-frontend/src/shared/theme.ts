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
                    main: '#424242',
                    contrastText: '#ffffff',
                },
                secondary: {
                    main: '#616161',
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
                    main: '#e0e0e0',
                    contrastText: '#121212',
                },
                secondary: {
                    main: '#9e9e9e',
                    contrastText: '#121212',
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
        MuiBottomNavigation: {
            styleOverrides: {
                root: {
                    backgroundColor: '#2f2f2f',
                },
            },
        },
        MuiBottomNavigationAction: {
            styleOverrides: {
                root: {
                    color: '#9e9e9e',
                    '&.Mui-selected': {
                        color: '#ffffff',
                    },
                },
            },
        },
    },
});

export const createAppTheme = (mode: PaletteMode) => createTheme(getDesignTokens(mode));

// For backwards compatibility, export default theme in light mode
const theme = createAppTheme('light');
export default theme;
