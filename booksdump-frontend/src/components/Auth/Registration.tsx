// src/components/Auth/Registration.tsx
import React, { useState, useEffect } from 'react';
import { Button, Typography, CardContent, CardActions, Box, IconButton } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';
import { API_URL } from '../../api/config';
import ArrowBackIcon from "@mui/icons-material/ArrowBack";
import { StyledTextField } from "../StyledDataItems";

const Registration: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [email, setEmail] = useState('');
    const [invite, setInvite] = useState('');
    const [regError, setRegError] = useState('');
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
            const response = await fetch(`${API_URL}/api/register`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(userData),
            });

            if (!response.ok) {
                const errorData = await response.json();
                const errorMessages: Record<string, string> = {
                    bad_invite: t('badInvite'),
                    bad_form: t('badForm'),
                    user_exists: t('userExists')
                };
                const errorMessage = errorMessages[errorData.message] || t('registrationError');
                setRegError(errorMessage);
                return;
            }

            // Handle successful registration here
        } catch (error) {
            console.error('Error registering:', error);
            setRegError(t('registrationError'));
        }
    };

    return (
        <LoginCenteredBox>
            <CardContent>
                <Typography variant="h4" textAlign="center">{t('registration')}</Typography>
                <StyledTextField
                    label={t('username')}
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
        </LoginCenteredBox>
    );
};

export default Registration;