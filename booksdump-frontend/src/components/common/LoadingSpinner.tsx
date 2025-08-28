import React from 'react';
import { Box, CircularProgress, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';

interface LoadingSpinnerProps {
    message?: string;
    size?: number;
}

const LoadingSpinner: React.FC<LoadingSpinnerProps> = ({ 
    message, 
    size = 40 
}) => {
    const { t } = useTranslation();

    return (
        <Box
            display="flex"
            flexDirection="column"
            alignItems="center"
            justifyContent="center"
            minHeight="200px"
            gap={2}
        >
            <CircularProgress size={size} />
            {message && (
                <Typography variant="body2" color="textSecondary">
                    {t(message)}
                </Typography>
            )}
        </Box>
    );
};

export default LoadingSpinner;
