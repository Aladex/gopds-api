import React, {useState, useEffect} from 'react';
import {
    AppBar,
    Toolbar,
    Typography,
    Button,
    Tabs,
    Tab,
    Box,
    IconButton,
    Drawer,
    List,
    ListItemText,
    ListItemButton
} from '@mui/material';
import {useNavigate} from 'react-router-dom';
import {useAuth} from '../../context/AuthContext';
import {useTheme} from '@mui/material/styles';
import {useMediaQuery} from '@mui/material';
import axios from 'axios';
import {API_URL} from '../../api/config';
import {useTranslation} from 'react-i18next';
import {Menu as MenuIcon, Logout, Person} from "@mui/icons-material";

const Header: React.FC = () => {
    const {logout, token, updateUser, user} = useAuth();
    const navigate = useNavigate();
    const theme = useTheme();
    const {t} = useTranslation();
    const [value, setValue] = useState(0);
    const isMobile = useMediaQuery('(max-width:600px)');
    const [drawerOpen, setDrawerOpen] = useState(false);

    useEffect(() => {
        const fetchUser = async () => {
            try {
                const response = await axios.get(`${API_URL}/books/self-user`, {
                    headers: {Authorization: `${token}`},
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

    const handleUserInfo = () => {
        // Add user info dialog window
    };

    const a11yProps = (index: number) => {
        return {
            id: `simple-tab-${index}`,
            'aria-controls': `simple-tabpanel-${index}`,
        };
    };

    const handleDrawerToggle = () => {
        setDrawerOpen(!drawerOpen);
    };

    const menuItems = [
        {label: t('booksTab'), path: '/books/page/1', index: 0},
        {label: t('opdsTab'), path: '/catalog', index: 1},
        {label: t('donateTab'), path: '/donate', index: 2},
    ];

    if (user?.is_superuser) {
        menuItems.push({label: t('adminTab'), path: '/admin', index: 3});
    }

    return (
        <AppBar position="static" sx={{bgcolor: theme.palette.secondary.main}}>
            <Toolbar>
                {isMobile ? (
                    <>
                        <Box display="flex" justifyContent="space-between" alignItems="center" width="100%">
                            <Box display="flex" justifyContent="space-between" alignItems="center" width="100%">
                                <Box display="flex" alignItems="center">
                                    <IconButton edge="start" color="inherit" aria-label="menu" onClick={handleDrawerToggle}>
                                        <MenuIcon color="primary"/>
                                    </IconButton>
                                </Box>
                                <Box display="flex" alignItems="center">
                                    <Button sx={{color: theme.palette.primary.main}} onClick={handleUserInfo}>
                                        <Person />
                                    </Button>
                                    <Button sx={{color: theme.palette.primary.main}} onClick={handleLogout}>
                                        <Logout/>
                                    </Button>
                                </Box>
                            </Box>
                            <Drawer
                                anchor="left"
                                open={drawerOpen}
                                onClose={handleDrawerToggle}
                                ModalProps={{
                                    keepMounted: true, // Better open performance on mobile.
                                }}
                                PaperProps={{
                                    sx: {
                                        width: '50%', // Occupy the full width of the screen
                                    }
                                }}
                            >
                                <List>
                                    {menuItems.map((item, index) => (
                                        <ListItemButton key={index} onClick={() => {
                                            navigate(item.path);
                                            handleDrawerToggle();
                                        }}>
                                            <ListItemText primary={item.label.toUpperCase()}/>
                                        </ListItemButton>
                                    ))}
                                    <ListItemButton onClick={handleUserInfo}>
                                        <ListItemText primary={user?.username}/>
                                    </ListItemButton>
                                </List>
                            </Drawer></Box>
                    </>
                ) : (
                    <>
                        <Typography variant="h6" sx={{flexGrow: 1, color: theme.palette.primary.main}}>
                            BOOKSDUMP
                        </Typography>
                        <Box sx={{borderBottom: 1, borderColor: 'divider'}}>
                            <Tabs value={value} onChange={handleChange} aria-label="basic tabs example">
                                {menuItems.map((item, index) => (
                                    <Tab
                                        key={index}
                                        label={item.label}
                                        {...a11yProps(item.index)}
                                        onClick={() => navigate(item.path)}
                                        sx={{color: value === item.index ? 'inherit' : '#9e9e9e'}}
                                    />
                                ))}
                            </Tabs>
                        </Box>
                        <Button sx={{color: theme.palette.primary.main}} onClick={handleUserInfo}>
                            {user?.username}
                        </Button>
                        <Button sx={{color: theme.palette.primary.main}} onClick={handleLogout}>
                            <IconButton color="inherit">
                                <Logout/>
                            </IconButton>
                        </Button>
                    </>
                )}
            </Toolbar>
        </AppBar>
    );
};

export default Header;
