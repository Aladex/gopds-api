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
        color: '#2f2f2f', // Цвет текста для неактивных кнопок
        backgroundColor: 'white', // Фон для неактивных кнопок
    },
    '& .MuiPaginationItem-root:hover': {
        backgroundColor: '#e0e0e0', // Цвет фона кнопок при наведении
    },
    '& .Mui-selected': {
        color: 'white', // Цвет текста для активной кнопки
        backgroundColor: theme.palette.secondary.main, // Фон для активной кнопки
    },
    '& .Mui-selected:hover': {
        backgroundColor: theme.palette.secondary.dark, // Цвет фона активной кнопки при наведении
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