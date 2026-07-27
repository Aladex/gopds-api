import React from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Typography, Container } from '@mui/material';
import {useTranslation} from 'react-i18next';

const ErrorPage: React.FC = () => {
    const navigate = useNavigate();
    const { t } = useTranslation();

    return (
        <Container maxWidth="sm" style={{ textAlign: 'center', marginTop: '50px' }}>
            <Typography variant="h4" gutterBottom>
                {t('errorPage.title')}
            </Typography>
            <Typography variant="subtitle1" gutterBottom>
                {t('errorPage.message')}
            </Typography>
            <Button variant="contained" color="primary" onClick={() => navigate('/')}>
                {t('errorPage.button')}
            </Button>
        </Container>
    );
};

export default ErrorPage;