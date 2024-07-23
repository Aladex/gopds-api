// src/components/Auth/ForgotPassword.tsx
import React, { useState } from 'react';
import {TextField, Button, Typography, CardContent, CardActions, IconButton, Box} from '@mui/material';
import { useNavigate } from 'react-router-dom';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';
import { API_URL } from '../../api/config';

const ForgotPassword: React.FC = () => {
    const [email, setEmail] = useState('');
    const navigate = useNavigate();
    const [resetError, setResetError] = useState('');
    const { t } = useTranslation();

    const handleReset = async () => {
        const resetData = {
            email,
        };

        try {
            const response = await fetch(`${API_URL}/api/change-request`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(resetData),
            });

            if (!response.ok) {
                const errorData = await response.json();
                const errorMessages: Record<string, string> = {
                    bad_form: t('badForm'),
                    invalid_user: t('invalidUser'),
                };
                const errorMessage = errorMessages[errorData.message] || t('resetError');
                setResetError(errorMessage);
                return;
            }

            navigate("/login");
        } catch (error) {
            console.error("Error resetting password:", error);
            setResetError(t('resetError'));
        }
    };


    return (
        <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h6" textAlign="center">{t('forgotPassword')}</Typography>
                    <TextField
                        label="Email"
                        type="email"
                        sx={{
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
                            },
                            '& .MuiInputLabel-root.Mui-focused': {
                                color: 'black',
                            },
                        }}
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        fullWidth
                        margin="normal"
                    />
                </CardContent>
                <CardActions>
                    <Box display="flex" justifyContent="space-between" width="100%">
                        <IconButton onClick={() => navigate('/login')} size="small" aria-label={t('BackButton')}>
                            <ArrowBackIcon />
                        </IconButton>
                        <Button variant="contained" color="primary" size="small" onClick={handleReset} disabled={!email}>
                            {t('resetPasswordButton')}
                        </Button>
                    </Box>
                </CardActions>
            {resetError && <Typography color="error" textAlign="center">{resetError}</Typography>}
        </LoginCenteredBox>
    );
};

export default ForgotPassword;
