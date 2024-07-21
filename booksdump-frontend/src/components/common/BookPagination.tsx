import React from 'react';
import { Pagination as MuiPagination } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { styled } from '@mui/material/styles';

interface PaginationProps {
    totalPages: number;
    currentPage: number;
}

const StyledPagination = styled(MuiPagination)(({ theme }) => ({
    '& .MuiPaginationItem-root': {
        fontWeight: 'bold',
        color: '#2f2f2f', // Background color for active button
        backgroundColor: 'white', // Text color for active button
    },
    '& .MuiPaginationItem-root:hover': {
        backgroundColor: '#e0e0e0', // Background color for hovered button
    },
    '& .Mui-selected': {
        color: 'white', // Text color for active button
        backgroundColor: theme.palette.secondary.main, // Background color for active button
    },
    '& .Mui-selected:hover': {
        backgroundColor: theme.palette.secondary.dark, // Background color for hovered button
    },
    '@media (max-width: 600px)': {
        '& .MuiPaginationItem-root': {
            minWidth: '24px', // Min width for button
            height: '24px', // Min height for button
            margin: '0 2px', // Margin between buttons
            fontSize: '0.75rem', // Font size for button
        },
    },
}));

const BookPagination: React.FC<PaginationProps> = ({ totalPages, currentPage }) => {
    const navigate = useNavigate();
    const handleChange = (event: React.ChangeEvent<unknown>, value: number) => {
        navigate(`/books/page/${value}`);
    };

    return (
        <StyledPagination
            count={totalPages}
            page={currentPage}
            onChange={handleChange}
            color="secondary"
            showFirstButton
            showLastButton
            variant="outlined" shape="rounded"
        />
    );
};

export default BookPagination;