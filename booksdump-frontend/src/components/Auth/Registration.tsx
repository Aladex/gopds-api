// src/components/Auth/Registration.tsx
import React, { useState } from 'react';
import { TextField, Button, Typography, CardContent, CardActions } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';

const Registration: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [email, setEmail] = useState('');
    const navigate = useNavigate();
    const { t } = useTranslation();

    const handleRegister = () => {
        console.log(`Registering user: ${username}, ${email}`);
    };

    return (
        <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h4" textAlign="center">{t('registration')}</Typography>
                    <TextField
                        label={t('username')}
                        value={username}
                        onChange={(e) => setUsername(e.target.value)}
                        fullWidth
                        margin="normal"
                    />
                    <TextField
                        label="Email"
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        fullWidth
                        margin="normal"
                    />
                    <TextField
                        label={t('password')}
                        type="password"
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        fullWidth
                        margin="normal"
                    />
                </CardContent>
                <CardActions>
                    <Button variant="contained" color="primary" fullWidth onClick={handleRegister}>
                        {t('registerButton')}
                    </Button>
                </CardActions>
                <CardActions>
                    <Button variant="text" fullWidth onClick={() => navigate('/login')}>
                        {t('login')}
                    </Button>
                </CardActions>
        </LoginCenteredBox>
    );
};

export default Registration;
