// src/components/common/Footer.tsx
import React from 'react';
import { Box } from '@mui/material';

const Footer: React.FC = () => {
    return (
        <Box
            component="footer"
            sx={{
                backgroundColor: '#1e1e1e',
                color: '#ffffff',
                padding: '2px 10px',
                width: '100%',
                position: 'relative',
                fontSize: '10px',
                marginTop: 'auto',
                minHeight: '8px',
            }}
        />
    );
};

export default Footer;
