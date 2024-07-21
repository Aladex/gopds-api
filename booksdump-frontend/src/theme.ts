// src/theme.ts
import { createTheme } from '@mui/material/styles';

const theme = createTheme({
    palette: {
        primary: {
            main: '#ffffff',
        },
        secondary: {
            main: '#2f2f2f',
        },
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
});

export default theme;
