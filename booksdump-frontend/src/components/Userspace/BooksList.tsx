// src/components/Userspace/Userspace.tsx
import React from 'react';
import { useParams } from 'react-router-dom';
import { Typography, Box } from '@mui/material';

const Userspace: React.FC = () => {
    const { page } = useParams<{ page: string }>();

    return (
        <Box>
            <Typography variant="h4">Books List - Page {page}</Typography>
            {/* Здесь можно добавить логику отображения списка книг */}
        </Box>
    );
};

export default Userspace;
