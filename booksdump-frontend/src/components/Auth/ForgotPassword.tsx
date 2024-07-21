// src/components/Auth/ForgotPassword.tsx
import React, { useState } from 'react';
import { TextField, Button, Typography, CardContent, CardActions, IconButton } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import LoginCenteredBox from "../common/CenteredBox";

const ForgotPassword: React.FC = () => {
    const [email, setEmail] = useState('');
    const navigate = useNavigate();

    const handleReset = () => {
        console.log(`Reset link sent to ${email}`);
    };

    return (
        <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h4" textAlign="center">Восстановление пароля</Typography>
                    <TextField
                        label="Email"
                        type="email"
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        fullWidth
                        margin="normal"
                    />
                </CardContent>
                <CardActions>
                    <Button variant="contained" color="primary" fullWidth onClick={handleReset}>
                        Тыц
                    </Button>
                </CardActions>
                <CardActions>
                    <IconButton onClick={() => navigate('/login')} aria-label="Назад">
                        <ArrowBackIcon />
                    </IconButton>
                </CardActions>
        </LoginCenteredBox>
    );
};

export default ForgotPassword;
