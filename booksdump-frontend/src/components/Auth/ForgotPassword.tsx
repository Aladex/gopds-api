// src/components/Auth/ForgotPassword.tsx
import React, { useState } from 'react';
import { Button, Typography, CardContent, CardActions, IconButton, Box} from '@mui/material';
import { useNavigate } from 'react-router-dom';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';
import { API_URL, fetchWithCsrf } from '../../api/config';
import { StyledTextField} from "../StyledDataItems";

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
            const response = await fetchWithCsrf(`${API_URL}/api/change-request`, {
                method: 'POST',
                body: JSON.stringify(resetData),
            });

            if (!response.ok) {
                const errorData = await response.json();
                const errorMessages: Record<string, string> = {
                    bad_form: t('badForm'),
                    invalid_user: t('invalidUser'),
                    'CSRF token invalid': t('csrfTokenInvalid') || 'CSRF token invalid',
                };
                const errorMessage = errorMessages[errorData.error] || errorMessages[errorData.message] || t('resetError');
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
                    <StyledTextField
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
