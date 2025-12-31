import { styled, Theme } from '@mui/material/styles';
import TextField from '@mui/material/TextField';
import FormControlUnstyled from '@mui/material/FormControl';

const commonStyles = (theme: Theme) => ({
    '& .MuiOutlinedInput-root': {
        boxShadow: theme.shadows[2],
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

const StyledTextField = styled(TextField)(({ theme }) => commonStyles(theme));

const StyledFormControl = styled(FormControlUnstyled)(({ theme }) => commonStyles(theme)); // Renamed to avoid conflict

export { StyledTextField, StyledFormControl };
