import React, { useState, useEffect } from 'react';
import { Button, Typography, CardContent, CardActions, Box, IconButton, InputAdornment } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../../context/AuthContext';
import { QuestionMark, Person, Lock } from '@mui/icons-material';
import { API_URL } from '../../api/config';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';
import { StyledTextField } from "../StyledDataItems";

const Login: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const { t } = useTranslation();
    const { setUser, isAuthenticated, isLoaded } = useAuth();
    const navigate = useNavigate();
    const [loginError, setLoginError] = useState('');

    // Redirect if user is already authenticated
    useEffect(() => {
        if (isLoaded && isAuthenticated) {
            navigate('/books/page/1');
        }
    }, [isAuthenticated, isLoaded, navigate]);

    // Don't render the form if user is authenticated or still loading
    if (!isLoaded || isAuthenticated) {
        return null; // or a loading spinner
    }

    const handleLogin = async () => {
        // Check if both username and password are entered
        if (!username || !password) {
            setLoginError(t('usernameAndPasswordRequired'));
            return; // Exit the function early if either is missing
        }

        try {
            const response = await fetch(`${API_URL}/api/login`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ username, password }),
            });

            if (response.ok) {
                const userData = await response.json();
                // Сразу устанавливаем пользователя из ответа login API
                setUser({
                    username: userData.username,
                    first_name: userData.first_name,
                    last_name: userData.last_name,
                    is_superuser: userData.is_superuser,
                    books_lang: userData.books_lang,
                    have_favs: userData.have_favs
                });
                navigate('/books/page/1');
            } else if (response.status === 403) {
                const errorData = await response.json();
                switch (errorData.message) {
                    case 'bad_credentials':
                        setLoginError(t('badCredentials'));
                        break;
                    case 'user not active':
                        setLoginError(t('userNotActive'));
                        break;
                    case 'bad password':
                        setLoginError(t('badPassword'));
                        break;
                    default:
                        setLoginError(t('loginOrPasswordIncorrect'));
                }
            } else {
                console.error('Error logging in:', response.statusText);
            }
        } catch (error) {
            console.error('Error logging in:', error);
        }
    };

    return (
        <LoginCenteredBox>
            <CardContent>
                <Typography variant="h6" textAlign="left">{t('login')}</Typography>
                {loginError && <Typography color="error">{loginError}</Typography>}
                <StyledTextField
                    label={t('username')}
                    onChange={(e) => setUsername(e.target.value)}
                    fullWidth
                    margin="normal"
                    InputProps={{
                        startAdornment: (
                            <InputAdornment position="start">
                                <Person />
                            </InputAdornment>
                        ),
                    }}
                />
                <StyledTextField
                    label={t('password')}
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    onKeyUp={(e) => {
                        if (e.key === 'Enter') {
                            handleLogin().then(r => r);
                        }
                    }}
                    fullWidth
                    margin="normal"
                    InputProps={{
                        startAdornment: (
                            <InputAdornment position="start">
                                <Lock />
                            </InputAdornment>
                        ),
                    }}
                />
            </CardContent>
            <CardActions>
                <Box display="flex" justifyContent="space-between" width="100%">
                    <IconButton onClick={() => navigate('/forgot-password')} aria-label={t('forgotPassword')} size="small">
                        <QuestionMark />
                    </IconButton>
                    <Button variant="contained" disabled={!username || !password} color="primary" size="small"  onClick={handleLogin}>
                        {t('loginButton')}
                    </Button>
                </Box>
            </CardActions>
        </LoginCenteredBox>
    );
};

export default Login;