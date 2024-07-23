// src/components/Auth/Login.tsx
import React, { useState } from 'react';
import { TextField, Button, Typography, CardContent, CardActions, Box, IconButton, InputAdornment } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../../context/AuthContext';
import { QuestionMark, Person, Lock } from '@mui/icons-material';
import { API_URL } from '../../api/config';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';


const Login: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const { t } = useTranslation();
    const { login } = useAuth();
    const navigate = useNavigate();
    const [loginError, setLoginError] = useState('');

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
                const data = await response.json();
                login(data.token);
                navigate('/books/page/1');
            } else if (response.status === 403) {
                setLoginError(t('loginOrPasswordIncorrect')); // Assuming you have a translation key for the error message
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
                <TextField
                    label={t('username')}
                    value={username}
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
                <TextField
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
