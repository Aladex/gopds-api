// src/components/Auth/Registration.tsx
import React, { useState, useEffect } from 'react';
import {TextField, Button, Typography, CardContent, CardActions, Box, IconButton} from '@mui/material';
import { useNavigate } from 'react-router-dom';
import LoginCenteredBox from "../common/CenteredBox";
import { useTranslation } from 'react-i18next';
import ArrowBackIcon from "@mui/icons-material/ArrowBack";

const Registration: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [email, setEmail] = useState('');
    const [invite, setInvite] = useState('');
    const navigate = useNavigate();
    const { t } = useTranslation();
    const [isFormValid, setIsFormValid] = useState(false);

    const handleRegister = () => {
        console.log(`Registering user: ${username}, ${email}`);
    };

    useEffect(() => {
        setIsFormValid(username !== '' && password !== '' && email !== '' && invite !== '');
    }, [username, password, email, invite]);

    return (
        <LoginCenteredBox>
                <CardContent>
                    <Typography variant="h4" textAlign="center">{t('registration')}</Typography>
                    <TextField
                        label={t('username')}
                        sx={{
                            '& .MuiOutlinedInput-root': {
                                '& fieldset': {
                                    borderColor: 'rgba(0, 0, 0, 0.23)',
                                },
                                '&:hover fieldset': {
                                    borderColor: 'black',
                                },
                                '&.Mui-focused fieldset': {
                                    borderColor: 'black',
                                },
                            },
                            '& .MuiInputLabel-root': {
                                color: 'rgba(0, 0, 0, 0.6)',
                            },
                            '& .MuiInputLabel-root.Mui-focused': {
                                color: 'black',
                            },
                        }}
                        value={username}
                        onChange={(e) => setUsername(e.target.value)}
                        fullWidth
                        margin="normal"
                        required
                    />
                    <TextField
                        label="Email"
                        sx={{
                            '& .MuiOutlinedInput-root': {
                                '& fieldset': {
                                    borderColor: 'rgba(0, 0, 0, 0.23)',
                                },
                                '&:hover fieldset': {
                                    borderColor: 'black',
                                },
                                '&.Mui-focused fieldset': {
                                    borderColor: 'black',
                                },
                            },
                            '& .MuiInputLabel-root': {
                                color: 'rgba(0, 0, 0, 0.6)',
                            },
                            '& .MuiInputLabel-root.Mui-focused': {
                                color: 'black',
                            },
                        }}
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        fullWidth
                        margin="normal"
                        required
                    />
                    <TextField
                        label={t('password')}
                        sx={{
                            '& .MuiOutlinedInput-root': {
                                '& fieldset': {
                                    borderColor: 'rgba(0, 0, 0, 0.23)',
                                },
                                '&:hover fieldset': {
                                    borderColor: 'black',
                                },
                                '&.Mui-focused fieldset': {
                                    borderColor: 'black',
                                },
                            },
                            '& .MuiInputLabel-root': {
                                color: 'rgba(0, 0, 0, 0.6)',
                            },
                            '& .MuiInputLabel-root.Mui-focused': {
                                color: 'black',
                            },
                        }}
                        type="password"
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        fullWidth
                        margin="normal"
                        required
                    />
                    <TextField
                        label={t('inviteCode')}
                        sx={{
                            '& .MuiOutlinedInput-root': {
                                '& fieldset': {
                                    borderColor: 'rgba(0, 0, 0, 0.23)',
                                },
                                '&:hover fieldset': {
                                    borderColor: 'black',
                                },
                                '&.Mui-focused fieldset': {
                                    borderColor: 'black',
                                },
                            },
                            '& .MuiInputLabel-root': {
                                color: 'rgba(0, 0, 0, 0.6)',
                            },
                            '& .MuiInputLabel-root.Mui-focused': {
                                color: 'black',
                            },
                        }}
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
                        <Button variant="contained" color="primary" size="small" onClick={handleRegister} disabled={!isFormValid} >
                            {t('registerButton')}
                        </Button>
                    </Box>
                </CardActions>
        </LoginCenteredBox>
    );
};

export default Registration;
