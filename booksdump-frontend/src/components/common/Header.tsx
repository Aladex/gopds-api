// src/components/Header.tsx
import React, { useState, useEffect } from 'react';
import { AppBar, Toolbar, Typography, Button, Tabs, Tab, Box } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../../context/AuthContext';
import { useTheme } from '@mui/material/styles';
import axios from 'axios';
import { API_URL } from '../../api/config';

const Header: React.FC = () => {
    const { logout, token, updateUser, user } = useAuth();
    const navigate = useNavigate();
    const theme = useTheme();
    const [value, setValue] = useState(0);

    useEffect(() => {
        const fetchUser = async () => {
            try {
                const response = await axios.get(`${API_URL}/books/self-user`, {
                    headers: { Authorization: `${token}` },
                });
                updateUser(response.data); // Update user data in context
            } catch (error) {
                console.error('Error fetching user data', error);
            }
        };

        fetchUser();
    }, [token, updateUser]);

    const handleLogout = () => {
        logout();
        navigate('/login');
    };

    const handleChange = (event: React.SyntheticEvent, newValue: number) => {
        setValue(newValue);
    };

    const a11yProps = (index: number) => {
        return {
            id: `simple-tab-${index}`,
            'aria-controls': `simple-tabpanel-${index}`,
        };
    };

    return (
        <AppBar position="static" sx={{ bgcolor: theme.palette.secondary.main }}>
            <Toolbar>
                <Typography variant="h6" sx={{ flexGrow: 1, color: theme.palette.primary.main }}>
                    BOOKSDUMP
                </Typography>
                <Box sx={{ borderBottom: 1, borderColor: 'divider' }}>
                    <Tabs value={value} onChange={handleChange} aria-label="basic tabs example">
                        <Tab label="КНИГИ" {...a11yProps(0)} onClick={() => navigate('/books')} sx={{ color: value === 0 ? 'inherit' : '#9e9e9e' }} />
                        <Tab label="OPDS" {...a11yProps(1)} onClick={() => navigate('/catalog')} sx={{ color: value === 1 ? 'inherit' : '#9e9e9e' }} />
                        <Tab label="ДОНАТ" {...a11yProps(2)} onClick={() => navigate('/donate')} sx={{ color: value === 2 ? 'inherit' : '#9e9e9e' }} />
                        {user?.is_superuser && <Tab label="АДМИН" {...a11yProps(3)} onClick={() => navigate('/admin')} sx={{ color: value === 3 ? 'inherit' : '#9e9e9e' }} />}
                        {user && <Tab label={user.username} {...a11yProps(4)} sx={{ color: value === 4 ? 'inherit' : '#9e9e9e' }} />}
                    </Tabs>
                </Box>
                <Button sx={{ color: theme.palette.primary.main }} onClick={handleLogout}>
                    ВЫХОД
                </Button>
            </Toolbar>
        </AppBar>
    );
};

export default Header;
