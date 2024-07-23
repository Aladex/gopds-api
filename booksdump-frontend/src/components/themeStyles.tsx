import { makeStyles } from '@mui/styles';
import { Theme } from '@mui/material/styles';

export const useCommonStyles = makeStyles((theme: Theme) => ({
    link: {
        color: theme.palette.secondary.main,
        textDecoration: 'none',
        '&:hover': {
            textDecoration: 'underline',
        },
    },
    buttonLink: {
        background: 'none',
        border: 'none',
        padding: 0,
        color: theme.palette.secondary.main,
        textDecoration: 'none',
        cursor: 'pointer',
        '&:hover': {
            textDecoration: 'underline',
        },
    },
}));