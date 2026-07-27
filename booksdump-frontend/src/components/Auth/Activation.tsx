import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useParams } from 'react-router-dom';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import { useTranslation } from 'react-i18next';
import { LinearProgress, Box, Typography } from '@mui/material';
import LoginCenteredBox from '../common/CenteredBox';
import CardContent from '@mui/material/CardContent';

const Activation: React.FC = () => {
    const navigate = useNavigate();
    const { t } = useTranslation();
    const { token } = useParams<{ token: string }>();
    const [progress, setProgress] = useState(0);

    useEffect(() => {
        const tokenValidation = async () => {
            try {
                await authApi.changePassword({ token: token ?? '', password: '' });
                setTimeout(() => {
                    navigate('/login');
                }, 5000);
            } catch (error) {
                // A missing or spent token is the expected failure here.
                if (isApiError(error) && error.isNotFound) {
                    navigate('/404');
                    return;
                }
                navigate('/404');
            }
        };
        tokenValidation();
    }, [token, navigate]);

    useEffect(() => {
        const timer = setInterval(() => {
            setProgress((oldProgress: number) => {
                if (oldProgress === 100) {
                    clearInterval(timer);
                    return 100;
                }
                return Math.min(oldProgress + 2, 100);
            });
        }, 100);

        return () => {
            clearInterval(timer);
        };
    }, []);

    return (
        <LoginCenteredBox>
            <CardContent>
                <Typography variant="h6" textAlign="center">{t('activation')}</Typography>
                <Box textAlign="center">
                    <Typography>{t('activationSuccess')}</Typography>
                    <LinearProgress variant="determinate" value={progress} color={"secondary"} />
                </Box>
            </CardContent>
        </LoginCenteredBox>
    );
};

export default Activation;