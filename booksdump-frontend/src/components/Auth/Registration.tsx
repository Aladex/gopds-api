// src/components/Auth/Registration.tsx
import React, { useState } from 'react';
import { TextField, Button, Typography, CardContent, CardActions } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import LoginCenteredBox from "../common/CenteredBox";


const Registration: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [email, setEmail] = useState('');
    const navigate = useNavigate();

    const handleRegister = () => {
        console.log(`Registering user: ${username}, ${email}`);
    };

    return (
        <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h4" textAlign="center">Регистрация</Typography>
                    <TextField
                        label="Логин"
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
                        label="Пароль"
                        type="password"
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        fullWidth
                        margin="normal"
                    />
                </CardContent>
                <CardActions>
                    <Button variant="contained" color="primary" fullWidth onClick={handleRegister}>
                        Зарегистрироваться
                    </Button>
                </CardActions>
                <CardActions>
                    <Button variant="text" fullWidth onClick={() => navigate('/login')}>
                        Вернуться к авторизации
                    </Button>
                </CardActions>
        </LoginCenteredBox>
    );
};

export default Registration;
