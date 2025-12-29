import { SxProps, Theme } from '@mui/material/styles';

// Common link styles using sx prop
export const linkSx: SxProps<Theme> = (theme) => ({
    color: theme.palette.secondary.main,
    textDecoration: 'none',
    '&:hover': {
        textDecoration: 'underline',
    },
});

// Common button link styles using sx prop
export const buttonLinkSx: SxProps<Theme> = (theme) => ({
    background: 'none',
    border: 'none',
    padding: 0,
    color: theme.palette.secondary.main,
    textDecoration: 'none',
    cursor: 'pointer',
    fontFamily: 'inherit',
    fontSize: 'inherit',
    '&:hover': {
        textDecoration: 'underline',
    },
});

// Form input styles using sx prop
export const formInputSx: SxProps<Theme> = {
    '& .MuiOutlinedInput-root': {
        '& fieldset': {
            borderColor: 'rgba(0, 0, 0, 0.23)',
        },
        '&:hover fieldset': {
            borderColor: 'black',
        },
        '&.Mui-focused fieldset': {
            borderColor: 'black',
        },
    },
    '& .MuiInputLabel-root': {
        color: 'rgba(0, 0, 0, 0.6)',
        '&.Mui-focused': {
            color: 'black',
        },
    },
};