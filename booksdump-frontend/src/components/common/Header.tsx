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
import { fetchWithAuth} from '../../api/config';
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
    const [showPasswordFields, setShowPasswordFields] = useState(false);
    const [firstName, setFirstName] = useState(user?.first_name || '');
    const [lastName, setLastName] = useState(user?.last_name || '');
    const [oldPassword, setOldPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');

    useEffect(() => {
        if (user) {
            setFirstName(user.first_name || '');
            setLastName(user.last_name || '');
        }
    }, [user]); // Зависимость от user гарантирует, что useEffect сработает при его изменении

    useEffect(() => {
        const fetchUser = async () => {
            try {
                const response = await fetchWithAuth.get(`/books/self-user`);
                if (response.status === 200) {
                    const data = await response.data; // Correctly parse the JSON response
                    updateUser(data); // Update user data in context
                } else {
                    console.error('Failed to fetch user data');
                }
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
        setShowPasswordFields(false);
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

    const togglePasswordFields = () => {
        setShowPasswordFields(!showPasswordFields);
    };

    const handleUserChange = async () => {
        handleDialogClose();
        try {
            const userData = {
                username: user?.username, // Assuming the username is from the user context
                first_name: firstName,
                last_name: lastName,
                new_password: newPassword,
                password: oldPassword
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
            const response = await fetchWithAuth(`/drop-sessions`, {
                headers: { Authorization: `${token}` },
            });
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
