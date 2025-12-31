import React, { useEffect } from 'react';
import { Route, Routes, Link, useLocation, useNavigate, Navigate } from 'react-router-dom';
import { Box, Card, CardContent, Tabs, Tab } from '@mui/material';
import UsersTable from './UsersTable';
import InvitesTable from './InvitesTable';

const AdminSpace: React.FC = () => {
    const location = useLocation();
    const navigate = useNavigate();
    const [value, setValue] = React.useState(location.pathname);

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
                                    TabIndicatorProps={{
                                        sx: {
                                            backgroundColor: (theme) => theme.palette.text.primary,
                                        },
                                    }}
                                >
                                    <Tab
                                        label="Users"
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
                                        label="Invites"
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
                                </Tabs>
                                <Box>
                                    <Routes>
                                        <Route path="users" element={<UsersTable />} />
                                        <Route path="users/:page" element={<UsersTable />} />
                                        <Route path="invites" element={<InvitesTable />} />
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
