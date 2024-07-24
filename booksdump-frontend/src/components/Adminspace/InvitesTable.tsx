import React from 'react';
import { useParams } from 'react-router-dom';
import { Typography, Box } from '@mui/material';

const InvitesTable: React.FC = () => {
    const { page } = useParams<{ page: string }>();

    return (
        <Box>
            <Typography variant="h6" align="center">Invites Table - Page {page}</Typography>
            {/* Здесь будет ваша таблица с инвайтами */}
        </Box>
    );
};

export default InvitesTable;
