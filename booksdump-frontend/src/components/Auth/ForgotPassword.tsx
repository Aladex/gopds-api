// src/components/Auth/ForgotPassword.tsx
import React, { useState } from 'react';
import {TextField, Button, Typography, CardContent, CardActions, IconButton, Box} from '@mui/material';
import { useNavigate } from 'react-router-dom';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';

const ForgotPassword: React.FC = () => {
    const [email, setEmail] = useState('');
    const navigate = useNavigate();
    const { t } = useTranslation();

    const handleReset = () => {
        console.log(`Reset link sent to ${email}`);
    };

    return (
        <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h6" textAlign="center">{t('forgotPassword')}</Typography>
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
                    <Box display="flex" justifyContent="space-between" width="100%">
                        <IconButton onClick={() => navigate('/login')} size="small" aria-label="Назад">
                            <ArrowBackIcon />
                        </IconButton>
                        <Button variant="contained" color="primary" size="small" onClick={handleReset} disabled={!email}>
                            {t('resetPasswordButton')}
                        </Button>
                    </Box>
                </CardActions>
        </LoginCenteredBox>
    );
};

export default ForgotPassword;
