// src/components/common/CenteredBox.tsx
import React from 'react';
import { Box, Card } from '@mui/material';

interface ILoginCenteredBoxProps {
    children: React.ReactNode;
}

const LoginCenteredBox: React.FC<ILoginCenteredBoxProps> = ({ children }) => {
    return (
        <Box display="flex" justifyContent="center" alignItems="center" height="100vh">
            <Card sx={{ minWidth: 275 }}>
                {children}
            </Card>
        </Box>
    );
};

export default LoginCenteredBox;