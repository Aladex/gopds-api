import React from 'react';
import { Box, Card, useMediaQuery } from '@mui/material';
import theme from '../../theme';
import { Link } from 'react-router-dom';

interface ILoginCenteredBoxProps {
    children: React.ReactNode;
}

const LoginCenteredBox: React.FC<ILoginCenteredBoxProps> = ({ children }) => {
    const isMobile = useMediaQuery(theme.breakpoints.down('sm'));

    return (
        <Box display="flex" justifyContent="center" alignItems="center" height="100vh">
            <Card sx={{ padding: 3, [theme.breakpoints.up('md')]: { minWidth: 700 } }}>
                {children}
            </Card>
            <Link to="/registration" style={{ position: 'absolute', bottom: 0, right: 0 }}>
                <img
                    src="/devito_back.png"
                    alt="Registration"
                    style={{
                        width: isMobile ? '100px' : '378px',
                        height: isMobile ? '68px' : '256px',
                        display: 'block',
                        border: 'none'
                    }}
                />
            </Link>
        </Box>
    );
};

export default LoginCenteredBox;