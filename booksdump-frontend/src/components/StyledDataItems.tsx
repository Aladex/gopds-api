import { styled } from '@mui/material/styles';
import TextField from '@mui/material/TextField';
import FormControlUnstyled from '@mui/material/FormControl';

const commonStyles = {
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

const StyledTextField = styled(TextField)(commonStyles);

const StyledFormControl = styled(FormControlUnstyled)(commonStyles); // Renamed to avoid conflict

export { StyledTextField, StyledFormControl };