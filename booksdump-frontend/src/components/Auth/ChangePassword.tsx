import React, { useState } from 'react';
import { Button, Typography, CardContent, CardActions, IconButton, Box } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';
import { API_URL } from '../../api/config';
import { StyledTextField } from "../StyledDataItems";
import { useParams } from 'react-router-dom';

const ChangePassword: React.FC = () => {
    const [newPassword, setNewPassword] = useState('');
    const navigate = useNavigate();
    const [changeError, setChangeError] = useState('');
    const { t } = useTranslation();
    const { token } = useParams<{ token: string }>();

    const handleChangePassword = async () => {
        const changeData = {
            password: newPassword,
            token: token
        };

        try {
            const response = await fetch(`${API_URL}/api/token`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(changeData),
            });

            // If response is 404 then redirect to /404
            if (response.status === 404) {
                navigate("/404");
                return;
            }

            // If not 200 then show error message
            if (!response.ok) {
                const errorData = await response.json();
                const errorMessages: Record<string, string> = {
                    bad_form: t('badForm'),
                    invalid_user: t('invalidUser'),
                };
                const errorMessage = errorMessages[errorData.message] || t('changePasswordError');
                setChangeError(errorMessage);
                return;
            }


            navigate("/login");
        } catch (error) {
            console.error("Error changing password:", error);
            setChangeError(t('changePasswordError'));
        }
    };

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
                    <Button variant="contained" color="primary" size="small" onClick={handleChangePassword} disabled={!newPassword}>
                        {t('changePasswordButton')}
                    </Button>
                </Box>
            </CardActions>
            {changeError && <Typography color="error" textAlign="center">{changeError}</Typography>}
        </LoginCenteredBox>
    );
};

export default ChangePassword;