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
    useMediaQuery,
    Select,
    MenuItem,
    FormControl,
    Menu
} from '@mui/material';
import {useAuth} from '../../context/AuthContext';
import {useTheme} from '@mui/material/styles';
import {fetchWithAuth} from '../../api/config';
import {useTranslation} from 'react-i18next';
import {Logout, Menu as MenuIcon, Person, VolunteerActivism} from "@mui/icons-material";
import {StyledTextField} from "../StyledDataItems";
import {useCommonStyles} from "../themeStyles";
import {useFav} from "../../context/FavContext";
import { useSearchBar } from "../../context/SearchBarContext";
import { getLanguageDisplaySafe } from "../../utils/languageUtils";
import DonateModal from "./DonateModal";

const Header: React.FC = () => {
    const {logout, updateUser, user, updateLang} = useAuth();
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
    const { languages, selectedLanguage, setSelectedLanguage } = useSearchBar();
    const [languageMenuAnchor, setLanguageMenuAnchor] = useState<null | HTMLElement>(null);
    const [isDonateModalOpen, setIsDonateModalOpen] = useState<boolean>(false);

    // Filter languages to only show those that are supported and have proper display
    const supportedLanguages = languages.filter(lang => {
        const display = getLanguageDisplaySafe(lang);
        return display !== null;
    });

    const updateLangAndSelectedLanguage = (lang: string) => {
        updateLang(lang);
        setSelectedLanguage(lang);
        setLanguageMenuAnchor(null); // Закрываем меню после выбора
    };

    const handleLanguageMenuOpen = (event: React.MouseEvent<HTMLElement>) => {
        setLanguageMenuAnchor(event.currentTarget);
    };

    const handleLanguageMenuClose = () => {
        setLanguageMenuAnchor(null);
    };

    const menuItems = useMemo(() => {
        const items = [
            { label: t('booksTab'), path: '/books/page/1', regex: /^\/books\/page\/\d+/, index: 0 },
            { label: t('opdsTab'), path: '/catalog', regex: /^\/catalog/, index: 1 }
        ];
        if (user?.is_superuser) {
            items.push({ label: t('adminTab'), path: '/admin', regex: /^\/admin/, index: 2 });
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
                            <Box display="flex" alignItems="center" gap={0.5}>
                                {/* Псевдо-вкладка доната для мобильной версии - СЛЕВА */}
                                <Box
                                    onClick={() => setIsDonateModalOpen(true)}
                                    sx={{
                                        display: 'flex',
                                        alignItems: 'center',
                                        height: '32px',
                                        padding: '4px 6px',
                                        cursor: 'pointer',
                                        color: '#9e9e9e',
                                        fontSize: '0.7rem',
                                        fontWeight: 500,
                                        textTransform: 'uppercase',
                                        minWidth: 'fit-content',
                                        justifyContent: 'center',
                                        borderRadius: '4px',
                                        gap: '2px',
                                        '&:hover': {
                                            color: '#fff',
                                            backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                        },
                                    }}
                                >
                                    <VolunteerActivism sx={{ fontSize: '1rem' }} />
                                    ДОНАТ
                                </Box>
                                {/* Псевдо-вкладка языка для мобильной версии */}
                                <Box
                                    onClick={handleLanguageMenuOpen}
                                    sx={{
                                        display: 'flex',
                                        alignItems: 'center',
                                        height: '32px',
                                        padding: '4px 6px',
                                        cursor: 'pointer',
                                        color: '#9e9e9e',
                                        fontSize: '0.7rem',
                                        fontWeight: 500,
                                        textTransform: 'uppercase',
                                        minWidth: '50px',
                                        justifyContent: 'center',
                                        borderRadius: '4px',
                                        '&:hover': {
                                            color: '#fff',
                                            backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                        },
                                    }}
                                >
                                    {selectedLanguage ? getLanguageDisplaySafe(selectedLanguage) : t('language')}
                                </Box>
                                {/* Существующие меню языка */}
                                <Menu
                                    anchorEl={languageMenuAnchor}
                                    open={Boolean(languageMenuAnchor)}
                                    onClose={handleLanguageMenuClose}
                                    anchorOrigin={{
                                        vertical: 'bottom',
                                        horizontal: 'center',
                                    }}
                                    transformOrigin={{
                                        vertical: 'top',
                                        horizontal: 'center',
                                    }}
                                    slotProps={{
                                        paper: {
                                            sx: {
                                                '& .MuiMenuItem-root': {
                                                    fontSize: '0.75rem',
                                                    minHeight: '32px',
                                                    paddingY: '4px',
                                                    paddingX: '8px',
                                                },
                                            }
                                        }
                                    }}
                                >
                                    {supportedLanguages.map((lang) => (
                                        <MenuItem
                                            key={lang}
                                            onClick={() => updateLangAndSelectedLanguage(lang)}
                                            selected={selectedLanguage === lang}
                                        >
                                            {getLanguageDisplaySafe(lang)}
                                        </MenuItem>
                                    ))}
                                </Menu>
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
                            sx={{
                                '& .MuiDrawer-paper': {
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
                        <Box sx={{borderBottom: 1, borderColor: 'divider', display: 'flex', alignItems: 'center'}}>
                            {/* Псевдо-вкладка доната для десктопно�� версии - СЛЕВА */}
                            <Box
                                onClick={() => setIsDonateModalOpen(true)}
                                sx={{
                                    display: 'flex',
                                    alignItems: 'center',
                                    height: '48px',
                                    padding: '6px 8px',
                                    cursor: 'pointer',
                                    color: '#9e9e9e',
                                    fontSize: '0.875rem',
                                    fontWeight: 500,
                                    textTransform: 'uppercase',
                                    minWidth: 'fit-content',
                                    justifyContent: 'center',
                                    gap: '4px',
                                    '&:hover': {
                                        color: '#fff',
                                        backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                    },
                                }}
                            >
                                <VolunteerActivism sx={{ fontSize: '1.2rem' }} />
                                ДОНАТ
                            </Box>
                            {/* Вкладка языка - отдельно от системы Tabs, чтобы не мешать подчеркиванию */}
                            <Box
                                onClick={handleLanguageMenuOpen}
                                sx={{
                                    display: 'flex',
                                    alignItems: 'center',
                                    height: '48px',
                                    padding: '6px 8px',
                                    cursor: 'pointer',
                                    color: '#9e9e9e',
                                    fontSize: '0.875rem',
                                    fontWeight: 500,
                                    textTransform: 'uppercase',
                                    minWidth: 'fit-content',
                                    maxWidth: '120px',
                                    justifyContent: 'center',
                                    overflow: 'hidden',
                                    textOverflow: 'ellipsis',
                                    whiteSpace: 'nowrap',
                                    '&:hover': {
                                        color: '#fff',
                                        backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                    },
                                }}
                            >
                                {selectedLanguage ? getLanguageDisplaySafe(selectedLanguage) : t('language')}
                            </Box>
                            <Menu
                                anchorEl={languageMenuAnchor}
                                open={Boolean(languageMenuAnchor)}
                                onClose={handleLanguageMenuClose}
                                anchorOrigin={{
                                    vertical: 'bottom',
                                    horizontal: 'left',
                                }}
                                transformOrigin={{
                                    vertical: 'top',
                                    horizontal: 'left',
                                }}
                                slotProps={{
                                    paper: {
                                        sx: {
                                            '& .MuiMenuItem-root': {
                                                fontSize: '0.875rem',
                                                minHeight: '36px',
                                                paddingY: '6px',
                                                paddingX: '12px',
                                            },
                                        }
                                    }
                                }}
                            >
                                {supportedLanguages.map((lang) => (
                                    <MenuItem
                                        key={lang}
                                        onClick={() => updateLangAndSelectedLanguage(lang)}
                                        selected={selectedLanguage === lang}
                                    >
                                        {getLanguageDisplaySafe(lang)}
                                    </MenuItem>
                                ))}
                            </Menu>
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
                    <FormControl fullWidth margin="dense">
                        <Select
                            value={selectedLanguage}
                            onChange={(e) => updateLangAndSelectedLanguage(e.target.value)}
                            displayEmpty
                            inputProps={{ 'aria-label': 'Select language' }}
                        >
                            <MenuItem value="" disabled>
                                {t('selectLanguage')}
                            </MenuItem>
                            {supportedLanguages.map((lang) => (
                                <MenuItem key={lang} value={lang}>
                                    {getLanguageDisplaySafe(lang)}
                                </MenuItem>
                            ))}
                        </Select>
                    </FormControl>
                </DialogContent>
                <DialogActions>
                    <Button color={"secondary"} onClick={handleDialogClose}>{'Cancel'}</Button>
                    <Button color={"secondary"} onClick={handleUserChange}>{'Save'}</Button>
                </DialogActions>
            </Dialog>
            <DonateModal open={isDonateModalOpen} onClose={() => setIsDonateModalOpen(false)} />
        </AppBar>
    );
};

export default Header;
