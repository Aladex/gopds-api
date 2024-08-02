import React from 'react';
import { Pagination as MuiPagination, useMediaQuery, useTheme } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { styled } from '@mui/material/styles';

interface PaginationProps {
    totalPages: number;
    currentPage: number;
    baseUrl: string;
}

const StyledPagination = styled(MuiPagination)(({ theme }) => ({
    '& .MuiPaginationItem-root': {
        fontWeight: 'bold',
        color: '#2f2f2f',
        backgroundColor: 'white',
    },
    '& .MuiPaginationItem-root:hover': {
        backgroundColor: '#e0e0e0',
    },
    '& .Mui-selected': {
        color: 'white',
        backgroundColor: theme.palette.secondary.main,
    },
    '& .Mui-selected:hover': {
        backgroundColor: theme.palette.secondary.dark,
    },
    '@media (max-width: 600px)': {
        '& .MuiPaginationItem-root': {
            minWidth: '24px',
            height: '24px',
            margin: '0 2px',
            fontSize: '0.75rem',
        },
    },
}));

const getBaseUrl = (pathname: string) => {
    const pathSegments = pathname.split('/');
    if (pathSegments[pathSegments.length - 1].match(/^\d+$/)) {
        pathSegments.pop();
    }
    return pathSegments.join('/');
};

const BookPagination: React.FC<PaginationProps> = ({ totalPages, currentPage, baseUrl }) => {
    const navigate = useNavigate();
    const noPageUrl = getBaseUrl(baseUrl);
    const handleChange = (event: React.ChangeEvent<unknown>, value: number) => {
        navigate(`${noPageUrl}/${value}`);
    };

    const theme = useTheme();
    const isMobile = useMediaQuery(theme.breakpoints.down(780));

    return (
        <StyledPagination
            count={totalPages}
            page={currentPage}
            onChange={handleChange}
            color="secondary"
            showFirstButton={false}
            showLastButton={false}
            variant="outlined"
            shape="rounded"
            boundaryCount={isMobile ? 1 : 3}
            siblingCount={isMobile ? 1 : 3}
            sx={{
                '@media (max-width: 600px)': {
                    boundaryCount: 0,
                    siblingCount: 0,
                },
            }}
        />
    );
};

export default BookPagination;