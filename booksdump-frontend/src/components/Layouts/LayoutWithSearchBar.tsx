import React from 'react';
import { Outlet } from 'react-router-dom';
import SearchBar from '../common/SearchBar';
import { Box } from '@mui/material';

const LayoutWithSearchBar: React.FC = () => {
    return (
        <Box p={2}>
            <SearchBar />
            <Outlet />
        </Box>
    );
};

export default LayoutWithSearchBar;
