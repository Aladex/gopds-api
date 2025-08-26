import { styled } from '@mui/material/styles';
import TextField from '@mui/material/TextField';
import FormControlUnstyled from '@mui/material/FormControl';

const commonStyles = {
    '& .MuiOutlinedInput-root': {
        boxShadow: '0px 2px 1px -1px rgba(0,0,0,0.2), 0px 1px 1px 0px rgba(0,0,0,0.14), 0px 1px 3px 0px rgba(0,0,0,0.12)',
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