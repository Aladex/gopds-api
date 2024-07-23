import React, {useState, useEffect} from 'react';
import {Link} from 'react-router-dom';
import {
    AppBar,
    Toolbar,
    Dialog, DialogTitle, DialogContent, DialogActions,
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
import { StyledTextField } from "../StyledDataItems";
import { useCommonStyles } from "../themeStyles";

const Header: React.FC = () => {
    const {logout, token, updateUser, user} = useAuth();
    const navigate = useNavigate();
    const classes = useCommonStyles();
    const theme = useTheme();
    const {t} = useTranslation();
    const [value, setValue] = useState(0);
    const isMobile = useMediaQuery('(max-width:600px)');
    const [drawerOpen, setDrawerOpen] = useState(false);
    const [dialogOpen, setDialogOpen] = useState(false);

    useEffect(() => {
        const fetchUser = async () => {
            try {
                const response = await axios.get(`${API_URL}/api/books/self-user`, {
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
        setDialogOpen(true);
    };

    const handleDialogClose = () => {
        setDialogOpen(false);
    };

    const handleDrawerToggle = () => {
        setDrawerOpen(!drawerOpen);
    };

    const a11yProps = (index: number) => {
        return {
            id: `simple-tab-${index}`,
            'aria-controls': `simple-tabpanel-${index}`,
        };
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
                        <Typography sx={{ flexGrow: 1 }}>
                            <Link to="/books/page/1">
                                <img src="/logo.png" alt="Logo" style={{ width: 35, height: 35 }} />
                            </Link>
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
            <Dialog open={dialogOpen} onClose={handleDialogClose}>
                <DialogTitle>{(t('userInfo'))}</DialogTitle>
                <DialogContent>
                    <Box display="flex" justifyContent="space-between" marginBottom={2}>
                        <React.Fragment><button className={classes.buttonLink}>{(t('changePassword'))}</button></React.Fragment>
                        <React.Fragment><button className={classes.buttonLink}>{(t('dropSessions'))}</button></React.Fragment>
                    </Box>
                    <StyledTextField
                        autoFocus
                        margin="dense"
                        label={t('firstName')}
                        type="text"
                        fullWidth
                        value={user?.first_name}
                    />
                    <StyledTextField
                        margin="dense"
                        label={t('lastName')}
                        type="text"
                        fullWidth
                        value={user?.last_name}
                    />
                </DialogContent>
                <DialogActions>
                    <Button color={"secondary"} onClick={handleDialogClose}>{'Cancel'}</Button>
                    <Button color={"secondary"} onClick={handleDialogClose}>{'Save'}</Button>
                </DialogActions>
            </Dialog>
        </AppBar>
    );
};

export default Header;
