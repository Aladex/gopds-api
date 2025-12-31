import { SxProps, Theme } from '@mui/material/styles';

// Common link styles using sx prop
export const linkSx = (theme: Theme) => ({
    color: theme.palette.secondary.main,
    textDecoration: 'none',
    '&:hover': {
        textDecoration: 'underline',
    },
});

// Common button link styles using sx prop
export const buttonLinkSx = (theme: Theme) => ({
    background: 'none',
    border: 'none',
    padding: 0,
    color: theme.palette.text.primary,
    textDecoration: 'none',
    cursor: 'pointer',
    fontFamily: 'inherit',
    fontSize: 'inherit',
    '&:hover': {
        textDecoration: 'underline',
    },
});

// Form input styles using sx prop
export const formInputSx = (theme: Theme) => ({
    '& .MuiOutlinedInput-root': {
        '& fieldset': {
            borderColor: theme.palette.divider,
        },
        '&:hover fieldset': {
            borderColor: theme.palette.text.primary,
        },
        '&.Mui-focused fieldset': {
            borderColor: theme.palette.text.primary,
        },
    },
    '& .MuiInputLabel-root': {
        color: theme.palette.text.secondary,
        '&.Mui-focused': {
            color: theme.palette.text.primary,
        },
    },
});
