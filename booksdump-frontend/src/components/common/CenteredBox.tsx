// src/components/common/CenteredBox.tsx
import React from 'react';
import { Box, Card } from '@mui/material';
import theme from '../../theme';
import { Link } from 'react-router-dom';

interface ILoginCenteredBoxProps {
    children: React.ReactNode;
}

const LoginCenteredBox: React.FC<ILoginCenteredBoxProps> = ({ children }) => {
    return (
        <Box display="flex" justifyContent="center" alignItems="center" height="100vh">
            <Card sx={{ padding: 3, [theme.breakpoints.up('md')]: { minWidth: 700 } }}>
            {children}
            </Card>
            <Link to="/registration" style={{ position: 'absolute', bottom: 0, right: 0 }}>
                <img src="/devito_back.png" alt="Registration" style={{ width: 378, height: 256, display: 'block', border: 'none' }} />
            </Link>
        </Box>
    );
};

export default LoginCenteredBox;