import React from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Typography, Container } from '@mui/material';

const ErrorPage: React.FC = () => {
    const navigate = useNavigate();

    return (
        <Container maxWidth="sm" style={{ textAlign: 'center', marginTop: '50px' }}>
            <Typography variant="h4" gutterBottom>
                Oops! Something went wrong.
            </Typography>
            <Typography variant="subtitle1" gutterBottom>
                The page you are looking for might have been removed, had its name changed, or is temporarily unavailable.
            </Typography>
            <Button variant="contained" color="primary" onClick={() => navigate('/')}>
                Go to Homepage
            </Button>
        </Container>
    );
};

export default ErrorPage;