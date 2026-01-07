import React, { useEffect } from 'react';
import { Route, Routes, Link, useLocation, useNavigate, Navigate } from 'react-router-dom';
import { Box, Card, CardContent, Tabs, Tab, useMediaQuery, useTheme } from '@mui/material';
import UsersTable from './UsersTable';
import InvitesTable from './InvitesTable';
import Duplicates from './Duplicates';
import BookScanning from './BookScanning';
import { useTranslation } from 'react-i18next';

const AdminSpace: React.FC = () => {
    const location = useLocation();
    const navigate = useNavigate();
    const [value, setValue] = React.useState(location.pathname);
    const { t } = useTranslation();
    const theme = useTheme();
    const isMobile = useMediaQuery(theme.breakpoints.down('md'));

    useEffect(() => {
        if (location.pathname === '/admin') {
            navigate('/admin/users');
        } else {
            setValue(location.pathname);
        }
    }, [location.pathname, navigate]);

    const handleChange = (event: React.SyntheticEvent, newValue: string) => {
        setValue(newValue);
        navigate(newValue);
    };

    return (
        <Box p={2}>
            <Box maxWidth={1400} mx="auto">
                <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                    <CardContent>
                                <Tabs
                                    value={value}
                                    onChange={handleChange}
                                    aria-label="admin tabs"
                                    textColor="inherit"
                                    indicatorColor="primary"
                                    variant={isMobile ? 'scrollable' : 'standard'}
                                    scrollButtons="auto"
                                    allowScrollButtonsMobile
                                    slotProps={{
                                        indicator: {
                                            sx: {
                                                backgroundColor: (theme) => theme.palette.text.primary,
                                            },
                                        },
                                    }}
                                    sx={{
                                        '& .MuiTab-root': {
                                            padding: {
                                                xs: '12px 8px',
                                                sm: '12px 12px',
                                                md: '12px 16px',
                                            },
                                            minWidth: {
                                                xs: 'auto',
                                                md: 90,
                                            },
                                            fontSize: {
                                                xs: '0.875rem',
                                                md: '0.9375rem',
                                            },
                                        },
                                        '& .MuiTabs-scrollButtons': {
                                            '&.Mui-disabled': {
                                                opacity: 0.3,
                                            },
                                        },
                                    }}
                                >
                                    <Tab
                                        label={t('users')}
                                        value="/admin/users"
                                        component={Link}
                                        to="/admin/users"
                                        sx={{
                                            color: (theme) =>
                                                value.startsWith('/admin/users')
                                                    ? theme.palette.text.primary
                                                    : theme.palette.text.secondary,
                                            '&.Mui-selected': {
                                                color: (theme) => theme.palette.text.primary,
                                            },
                                        }}
                                    />
                                    <Tab
                                        label={t('invites')}
                                        value="/admin/invites"
                                        component={Link}
                                        to="/admin/invites"
                                        sx={{
                                            color: (theme) =>
                                                value.startsWith('/admin/invites')
                                                    ? theme.palette.text.primary
                                                    : theme.palette.text.secondary,
                                            '&.Mui-selected': {
                                                color: (theme) => theme.palette.text.primary,
                                            },
                                        }}
                                    />
                                    <Tab
                                        label={t('bookScanning')}
                                        value="/admin/book-scanning"
                                        component={Link}
                                        to="/admin/book-scanning"
                                        sx={{
                                            color: (theme) =>
                                                value.startsWith('/admin/book-scanning')
                                                    ? theme.palette.text.primary
                                                    : theme.palette.text.secondary,
                                            '&.Mui-selected': {
                                                color: (theme) => theme.palette.text.primary,
                                            },
                                        }}
                                    />
                                    <Tab
                                        label={t('duplicates')}
                                        value="/admin/duplicates"
                                        component={Link}
                                        to="/admin/duplicates"
                                        sx={{
                                            color: (theme) =>
                                                value.startsWith('/admin/duplicates')
                                                    ? theme.palette.text.primary
                                                    : theme.palette.text.secondary,
                                            '&.Mui-selected': {
                                                color: (theme) => theme.palette.text.primary,
                                            },
                                        }}
                                    />
                                </Tabs>
                                <Box>
                                    <Routes>
                                        <Route path="users" element={<UsersTable />} />
                                        <Route path="users/:page" element={<UsersTable />} />
                                        <Route path="invites" element={<InvitesTable />} />
                                        <Route path="book-scanning" element={<BookScanning />} />
                                        <Route path="duplicates" element={<Duplicates />} />
                                        <Route path="*" element={<Navigate to="/admin/users" />} />
                                    </Routes>
                                </Box>
                    </CardContent>
                </Card>
            </Box>
        </Box>
    );
};

export default AdminSpace;
