// src/components/common/Footer.tsx
import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { fetchWithAuth } from '../../api/config';

const Footer: React.FC = () => {
    const [appVersion, setAppVersion] = useState<string>('');

    useEffect(() => {
        const fetchAppVersion = async () => {
            try {
                const response = await fetchWithAuth.get('/status');
                setAppVersion(response.data.result);
            } catch (error) {
                console.error('Error fetching app version:', error);
            }
        };

        fetchAppVersion();
    }, []);

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
                display: 'flex',
                justifyContent: 'center',
                alignItems: 'center',
            }}
        >
            <Typography variant="caption" component="span">
                App Version: {appVersion}
            </Typography>
        </Box>
    );
};

export default Footer;
