import React, { useState, useEffect } from 'react';
import { Button, Typography, CardContent, CardActions, Box, IconButton } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import LoginCenteredBox from "@/components/common/CenteredBox";
import { useTranslation } from 'react-i18next';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import ArrowBackIcon from "@mui/icons-material/ArrowBack";
import { StyledTextField } from "@/components/StyledDataItems";

const Registration: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [email, setEmail] = useState('');
    const [invite, setInvite] = useState('');
    const [regError, setRegError] = useState('');
    const [isRegistered, setIsRegistered] = useState(false);
    const navigate = useNavigate();
    const { t } = useTranslation();
    const [isFormValid, setIsFormValid] = useState(false);

    useEffect(() => {
        setIsFormValid(username !== '' && password !== '' && email !== '' && invite !== '');
    }, [username, password, email, invite]);

    const handleRegister = async () => {
        const userData = {
            username,
            email,
            password,
            invite
        };

        try {
            await authApi.register(userData);
            setIsRegistered(true);
        } catch (error) {
            const errorMessages: Record<string, string> = {
                bad_invite: t('badInvite'),
                bad_form: t('badForm'),
                user_exists: t('userExists'),
                'CSRF token invalid': t('csrfTokenInvalid') || 'CSRF token invalid',
            };
            const body = isApiError(error)
                ? (error.body as { error?: string; message?: string } | undefined)
                : undefined;
            setRegError(
                errorMessages[body?.error ?? ''] ||
                    errorMessages[body?.message ?? ''] ||
                    t('registrationError'),
            );
        }
    };

    return (
        <LoginCenteredBox>
            {isRegistered ? (
                <>
                    <CardContent>
                        <Typography variant="h6" textAlign="center">
                            {t('registrationSuccess')}
                        </Typography>
                    </CardContent>
                    <CardActions>
                        <Box display="flex" justifyContent="space-between" width="100%">
                            <IconButton onClick={() => navigate('/login')} size="small" aria-label={t('BackButton')}>
                                <ArrowBackIcon />
                            </IconButton>
                        </Box>
                    </CardActions>
                </>
            ) : (
                <>
                    <CardContent>
                        <Typography variant="h4" textAlign="center">{t('registration')}</Typography>
                        <StyledTextField
                            label={t('usernameRegistration')}
                            value={username}
                            onChange={(e) => setUsername(e.target.value)}
                            fullWidth
                            margin="normal"
                            required
                        />
                        <StyledTextField
                            label="Email"
                            value={email}
                            onChange={(e) => setEmail(e.target.value)}
                            fullWidth
                            margin="normal"
                            required
                        />
                        <StyledTextField
                            label={t('password')}
                            type="password"
                            value={password}
                            onChange={(e) => setPassword(e.target.value)}
                            fullWidth
                            margin="normal"
                            required
                        />
                        <StyledTextField
                            label={t('inviteCode')}
                            value={invite}
                            onChange={(e) => setInvite(e.target.value)}
                            fullWidth
                            margin="normal"
                            required
                        />
                    </CardContent>
                    <CardActions>
                        <Box display="flex" justifyContent="space-between" width="100%">
                            <IconButton onClick={() => navigate('/login')} size="small" aria-label={t('BackButton')}>
                                <ArrowBackIcon />
                            </IconButton>
                            <Button variant="contained" color="primary" size="small" onClick={handleRegister} disabled={!isFormValid}>
                                {t('registerButton')}
                            </Button>
                        </Box>
                    </CardActions>
                    {regError && <Typography color="error" textAlign="center">{regError}</Typography>}
                </>
            )}
        </LoginCenteredBox>
    );
};

export default Registration;