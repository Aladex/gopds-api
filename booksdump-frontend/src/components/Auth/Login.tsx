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

    const handleLogin = async () => {
        try {
            const response = await fetch(`${API_URL}/login`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ username, password }),
            });

            if (response.ok) {
                const data = await response.json();
                console.log(data);
                login(data.token);
                navigate('/user');
            } else {
                console.error('Ошибка при авторизации');
            }
        } catch (error) {
            console.error('Ошибка сети:', error);
        }
    };

    return (
        <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h6" textAlign="left">{t('login')}</Typography>
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
