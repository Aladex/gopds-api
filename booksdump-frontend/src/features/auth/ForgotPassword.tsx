// src/components/Auth/ForgotPassword.tsx
import React, { useState } from 'react';
import { Button, Typography, CardContent, CardActions, IconButton, Box} from '@mui/material';
import { useNavigate } from 'react-router-dom';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import LoginCenteredBox from "@/shared/components/CenteredBox";
import { useTranslation } from 'react-i18next';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import { StyledTextField} from "@/shared/components/StyledDataItems";

const ForgotPassword: React.FC = () => {
    const [email, setEmail] = useState('');
    const navigate = useNavigate();
    const [resetError, setResetError] = useState('');
    const { t } = useTranslation();

    const handleReset = async () => {
        try {
            await authApi.requestPasswordChange(email);
            navigate("/login");
        } catch (error) {
            const errorMessages: Record<string, string> = {
                bad_form: t('badForm'),
                invalid_user: t('invalidUser'),
                'CSRF token invalid': t('csrfTokenInvalid') || 'CSRF token invalid',
            };
            const body = isApiError(error)
                ? (error.body as { error?: string; message?: string } | undefined)
                : undefined;
            setResetError(
                errorMessages[body?.error ?? ''] ||
                    errorMessages[body?.message ?? ''] ||
                    t('resetError'),
            );
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
