import React, { memo } from 'react';
import { Outlet } from 'react-router-dom';
import SearchBar from '../common/SearchBar';
import { Box } from '@mui/material';

const LayoutWithSearchBar: React.FC = memo(() => {
    return (
        <Box p={2}>
            <SearchBar />
            <Outlet />
        </Box>
    );
});

LayoutWithSearchBar.displayName = 'LayoutWithSearchBar';

export default LayoutWithSearchBar;
