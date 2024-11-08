import React, {useMemo, useEffect, useState} from 'react';
import {Link, useNavigate} from 'react-router-dom';
import {
    AppBar,
    Box,
    Button,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    Drawer,
    IconButton,
    List,
    ListItemButton,
    ListItemText,
    Tab,
    Tabs,
    Toolbar,
    Typography,
    useMediaQuery
} from '@mui/material';
import {useAuth} from '../../context/AuthContext';
import {useTheme} from '@mui/material/styles';
import {fetchWithAuth} from '../../api/config';
import {useTranslation} from 'react-i18next';
import {Logout, Menu as MenuIcon, Person} from "@mui/icons-material";
import {StyledTextField} from "../StyledDataItems";
import {useCommonStyles} from "../themeStyles";
import {useFav} from "../../context/FavContext";

const Header: React.FC = () => {
    const {logout, updateUser, user} = useAuth();
    const navigate = useNavigate();
    const classes = useCommonStyles();
    const theme = useTheme();
    const {t} = useTranslation();
    const [value, setValue] = useState(0);
    const isMobile = useMediaQuery('(max-width:600px)');
    const [drawerOpen, setDrawerOpen] = useState(false);
    const [dialogOpen, setDialogOpen] = useState(false);
    const [showPasswordFields, setShowPasswordFields] = useState(false);
    const [firstName, setFirstName] = useState(user?.first_name || '');
    const [lastName, setLastName] = useState(user?.last_name || '');
    const [oldPassword, setOldPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const {  setFav } = useFav();
    const menuItems = useMemo(() => {
        const items = [
            { label: t('booksTab'), path: '/books/page/1', regex: /^\/books\/page\/\d+/, index: 0 },
            { label: t('opdsTab'), path: '/catalog', regex: /^\/catalog/, index: 2 }
        ];
        if (user?.is_superuser) {
            items.push({ label: t('adminTab'), path: '/admin', regex: /^\/admin/, index: 3 });
        }
        return items;
    }, [t, user?.is_superuser]);

    useEffect(() => {
        if (user) {
            setFirstName(user.first_name || '');
            setLastName(user.last_name || '');
        }
    }, [user]);

    const handleLogout = () => {
        logout();
        navigate('/login');
    };

    useEffect(() => {
        const currentPath = window.location.pathname;
        const currentTab = menuItems.findIndex(item => item.regex.test(currentPath));
        if (currentTab !== -1) {
            setValue(currentTab);
            setFav(false); // Reset fav when changing tabs
        }
    }, [menuItems, setFav]);

    const handleChange = (event: React.SyntheticEvent, newValue: number) => {
        setValue(newValue);
        setFav(false); // Reset fav when changing tabs
    };

    const handleDrawerToggle = () => {
        setDrawerOpen(!drawerOpen);
    };

    const handleMenuItemClick = (path: string) => {
        navigate(path);
        setFav(false); // Reset fav when clicking menu items
        handleDrawerToggle();
    };


    const handleUserInfo = () => {
        setDialogOpen(true);
    };

    const handleDialogClose = () => {
        setDialogOpen(false);
        setShowPasswordFields(false);
    };

    const a11yProps = (index: number) => {
        return {
            id: `simple-tab-${index}`,
            'aria-controls': `simple-tabpanel-${index}`,
        };
    };

    const handleTabClick = (index: number, path: string) => {
        setValue(index);
        setFav(false); // Reset fav when clicking tabs
        navigate(path);
    };


    useEffect(() => {
        const currentPath = window.location.pathname;
        const currentTab = menuItems.findIndex(item => item.regex.test(currentPath));
        if (currentTab !== -1) {
            setValue(currentTab);
        }
    }, [menuItems]);

    const togglePasswordFields = () => {
        setShowPasswordFields(!showPasswordFields);
    };

    const handleUserChange = async () => {
        handleDialogClose();
        try {
            const userData = {
                username: user?.username,
                first_name: firstName,
                last_name: lastName,
                new_password: newPassword,
                password: oldPassword,
                books_lang: user?.books_lang,
            };
            // Send a POST request to the server to update the user data
            const response = await fetchWithAuth.post('/books/change-me', userData);

            if (response.status === 200) { // Check if the request was successful
                const data = response.data; // Parse the JSON response
                updateUser(data); //
            } else {
                console.error('Failed to update user');
            }
        } catch (error) {
            console.error('Error updating user:', error);
            // Handle the error here
        }
    };
    const handleDropSessions = async () => {
        try {
            const response = await fetchWithAuth(`/drop-sessions`);
            if (response.status === 200) {
                logout();
                navigate('/login');
            } else {
                console.error('Failed to drop sessions');
            }
        } catch (error) {
            console.error('Error dropping sessions:', error);
        }
    };

    const handleLogoClick = () => {
        setFav(false); // Reset fav when clicking the logo
    };

    return (
        <AppBar position="static" sx={{bgcolor: theme.palette.secondary.main}}>
            <Toolbar>
                {isMobile ? (
                    <>
                        <Box display="flex" justifyContent="space-between" alignItems="center" width="100%">
                            <Box display="flex" alignItems="center">
                                <IconButton edge="start" color="inherit" aria-label="menu" onClick={handleDrawerToggle}>
                                    <MenuIcon color="primary"/>
                                </IconButton>
                                <Link to="/books/page/1" onClick={handleLogoClick}>
                                    <img src="/logo.png" alt="Logo" style={{ width: 18, height: 18, marginTop: 6 }} />
                                </Link>
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
                                    width: '70%', // Occupy the full width of the screen
                                }
                            }}
                        >
                            <List>
                                {menuItems.map((item, index) => (
                                    <ListItemButton key={index} onClick={() => handleMenuItemClick(item.path)}>
                                        <ListItemText primary={item.label.toUpperCase()}/>
                                    </ListItemButton>
                                ))}
                                <ListItemButton onClick={handleUserInfo}>
                                    <ListItemText primary={user?.username}/>
                                </ListItemButton>
                            </List>
                        </Drawer>
                    </>
                ) : (
                    <>
                        <Typography sx={{ flexGrow: 1 }}>
                            <Link to="/books/page/1" onClick={handleLogoClick}>
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
                                        onClick={() => handleTabClick(item.index, item.path)}
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
                        <React.Fragment><button className={classes.buttonLink} onClick={togglePasswordFields}>
                            {(t('changePassword'))}
                        </button></React.Fragment>
                        <React.Fragment><button className={classes.buttonLink} onClick={handleDropSessions}>
                            {(t('dropSessions'))}
                        </button></React.Fragment>
                    </Box>
                    {showPasswordFields && (
                        <>
                            <StyledTextField
                                autoFocus
                                margin="dense"
                                label={t('oldPassword')}
                                type="password"
                                sx={{marginBottom: 2}}
                                fullWidth
                                value={oldPassword}
                                onChange={(e) => setOldPassword(e.target.value)}
                            />
                            <StyledTextField
                                margin="dense"
                                label={t('newPassword')}
                                type="password"
                                sx={{marginBottom: 2}}
                                fullWidth
                                value={newPassword}
                                onChange={(e) => setNewPassword(e.target.value)}
                            />
                        </>
                    )}
                    <StyledTextField
                        autoFocus
                        margin="dense"
                        label={t('firstName')}
                        type="text"
                        fullWidth
                        sx={{marginBottom: 2}}
                        value={firstName}
                        onChange={(e) => setFirstName(e.target.value)}
                    />
                    <StyledTextField
                        margin="dense"
                        label={t('lastName')}
                        type="text"
                        fullWidth
                        value={lastName}
                        onChange={(e) => setLastName(e.target.value)}
                    />
                </DialogContent>
                <DialogActions>
                    <Button color={"secondary"} onClick={handleDialogClose}>{'Cancel'}</Button>
                    <Button color={"secondary"} onClick={handleUserChange}>{'Save'}</Button>
                </DialogActions>
            </Dialog>
        </AppBar>
    );
};

export default Header;
