import React, { useState, useEffect } from 'react';
import { Button, Typography, CardContent, CardActions, IconButton, Box, CircularProgress } from '@mui/material';
import { useNavigate, useParams } from 'react-router-dom';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import { StyledTextField } from "../StyledDataItems";

const ChangePassword: React.FC = () => {
    const [newPassword, setNewPassword] = useState('');
    const [isValidating, setIsValidating] = useState(true);
    const [isChanging, setIsChanging] = useState(false);
    const [changeError, setChangeError] = useState('');
    const { t } = useTranslation();
    const { token } = useParams<{ token: string }>();
    const navigate = useNavigate();

    useEffect(() => {
        const tokenValidation = async () => {
            setIsValidating(true);
            try {
                await authApi.activate(token ?? '');
            } catch (error) {
                // An unknown or spent token is what a 404 means here.
                if (!isApiError(error) || error.isNotFound) {
                    navigate('/404');
                }
            } finally {
                setIsValidating(false);
            }
        };
        tokenValidation();
    }, [token, navigate]);

    const handleChangePassword = async () => {
        setIsChanging(true);
        setChangeError('');

        try {
            await authApi.changePassword({ token: token ?? '', password: newPassword });
            navigate('/login');
        } catch (error) {
            if (isApiError(error) && error.isNotFound) {
                navigate('/404');
                return;
            }
            const errorMessages: Record<string, string> = {
                bad_form: t('badForm'),
                invalid_user: t('invalidUser'),
                'CSRF token invalid': t('csrfTokenInvalid') || 'CSRF token invalid',
            };
            const body = isApiError(error)
                ? (error.body as { error?: string; message?: string } | undefined)
                : undefined;
            setChangeError(
                errorMessages[body?.error ?? ''] ||
                    errorMessages[body?.message ?? ''] ||
                    t('changePasswordError'),
            );
        } finally {
            setIsChanging(false);
        }
    };

    if (isValidating) {
        return (
            <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h6" textAlign="center">{t('validatingToken')}</Typography>
                    <Box display="flex" justifyContent="center" mt={2}>
                        <CircularProgress />
                    </Box>
                </CardContent>
            </LoginCenteredBox>
        );
    }

    return (
        <LoginCenteredBox>
            <CardContent>
                <Typography variant="h6" textAlign="center">{t('changePassword')}</Typography>
                <StyledTextField
                    label={t('newPassword')}
                    type="password"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    fullWidth
                    margin="normal"
                />
            </CardContent>
            <CardActions>
                <Box display="flex" justifyContent="space-between" width="100%">
                    <IconButton onClick={() => navigate('/login')} size="small" aria-label={t('BackButton')}>
                        <ArrowBackIcon />
                    </IconButton>
                    <Button variant="contained" color="primary" size="small" onClick={handleChangePassword} disabled={!newPassword || isChanging}>
                        {isChanging ? <CircularProgress size={24} /> : t('changePasswordButton')}
                    </Button>
                </Box>
            </CardActions>
            {changeError && <Typography color="error" textAlign="center">{changeError}</Typography>}
        </LoginCenteredBox>
    );
};

export default ChangePassword;