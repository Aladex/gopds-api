import React, {useEffect} from 'react';
import { useParams } from 'react-router-dom';
import { Typography, Box } from '@mui/material';

const UsersTable: React.FC = () => {
    const { page } = useParams<{ page: string }>();

    useEffect(() => {
        console.log('UsersTable page:', page);
    }, [page]);

    return (
        <Box>
            <Typography variant="h6" align="center">Users Table - Page {page}</Typography>
            {/* Здесь будет ваша таблица с пользователями */}
        </Box>
    );

};

export default UsersTable;
