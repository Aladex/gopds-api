import React, {useMemo, useEffect, useState} from 'react';
import {useNavigate} from 'react-router-dom';
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
    useMediaQuery,
    Select,
    MenuItem,
    FormControl,
    Menu
} from '@mui/material';
import {useAuth} from '../../context/AuthContext';
import {fetchWithAuth} from '../../api/config';
import {useTranslation} from 'react-i18next';
import {Logout, Menu as MenuIcon, Person, VolunteerActivism} from "@mui/icons-material";
import {StyledTextField} from "../StyledDataItems";
import {buttonLinkSx} from "../commonStyles";
import {useFav} from "../../context/FavContext";
import { useSearchBar } from "../../context/SearchBarContext";
import { getLanguageDisplaySafe, languageMapping } from "../../utils/languageUtils";
import DonateModal from "./DonateModal";
import ThemeToggle from "./ThemeToggle";

const Header: React.FC = () => {
    const {logout, updateUser, user, updateLang} = useAuth();
    const navigate = useNavigate();
    const {t, i18n} = useTranslation();
    const [value, setValue] = useState(0);
    const isMobile = useMediaQuery('(max-width:600px)');
    const isVeryNarrow = useMediaQuery('(max-width:354px)'); // Check for very narrow screens
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

    // Function to get language display based on screen width
    const getLanguageDisplay = (lang: string) => {
        if (isVeryNarrow) {
            // On very narrow screens show flag emoji + ISO code
            const languageInfo = languageMapping[lang];
            if (languageInfo) {
                return `${languageInfo.flag} ${lang.toUpperCase()}`;
            }
            return lang.toUpperCase(); // Fallback if no emoji available
        } else {
            // On regular screens show full name
            return getLanguageDisplaySafe(lang);
        }
    };

    const updateLangAndSelectedLanguage = (lang: string) => {
        updateLang(lang);
        setSelectedLanguage(lang);
        setLanguageMenuAnchor(null); // Close menu after selection
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [t, user?.is_superuser, i18n.language]); // i18n.language необходим для обновления переводов

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

    return (
        <AppBar
            position="static"
            sx={{ bgcolor: '#2f2f2f', color: '#ffffff', backgroundImage: 'none' }}
            style={{ backgroundColor: '#2f2f2f', color: '#ffffff', backgroundImage: 'none' }}
        >
            <Toolbar>
                {isMobile ? (
                    <>
                        <Box display="flex" justifyContent="space-between" alignItems="center" width="100%">
                            <Box display="flex" alignItems="center">
                                <IconButton edge="start" color="inherit" aria-label="menu" onClick={handleDrawerToggle}>
                                    <MenuIcon color="inherit"/>
                                </IconButton>
                            </Box>
                            <Box display="flex" alignItems="center" gap={0.5}>
                                {/* Pseudo donate tab for mobile version - LEFT */}
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
                                            color: '#ffffff',
                                            backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                        },
                                    }}
                                >
                                    <VolunteerActivism sx={{ fontSize: '1rem' }} />
                                    ДОНАТ
                                </Box>
                                {/* Pseudo language tab for mobile version */}
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
                                            color: '#ffffff',
                                            backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                        },
                                    }}
                                >
                                    {selectedLanguage ? getLanguageDisplay(selectedLanguage) : t('language')}
                                </Box>
                                {/* Existing language menu */}
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
                                            {getLanguageDisplay(lang)}
                                        </MenuItem>
                                    ))}
                                </Menu>
                                <ThemeToggle />
                                <Button sx={{ color: '#ffffff' }} onClick={handleLogout}>
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
                        {/* Left part - tabs with logo */}
                        <Box sx={{borderBottom: 1, borderColor: 'divider', display: 'flex', alignItems: 'center'}}>
                            <Tabs
                                value={value}
                                onChange={handleChange}
                                aria-label="basic tabs example"
                                textColor="inherit"
                                TabIndicatorProps={{ sx: { backgroundColor: '#ffffff' } }}
                            >
                                {menuItems.map((item, index) => (
                                    <Tab
                                        key={index}
                                        label={
                                            index === 0 ? (
                                                // For the first tab (BOOKS) add logo
                                                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                                    <img src="/logo.png" alt="Logo" style={{ width: 24, height: 24 }} />
                                                    {item.label}
                                                </Box>
                                            ) : (
                                                item.label
                                            )
                                        }
                                        {...a11yProps(item.index)}
                                        onClick={() => handleTabClick(item.index, item.path)}
                                        sx={{
                                            color: value === item.index ? '#ffffff' : '#9e9e9e',
                                            '&.Mui-selected': {
                                                color: '#ffffff',
                                            },
                                        }}
                                    />
                                ))}
                            </Tabs>
                            {/* Donate button after tabs */}
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
                                    marginLeft: 2,
                                    '&:hover': {
                                        color: '#ffffff',
                                        backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                    },
                                }}
                            >
                                <VolunteerActivism sx={{ fontSize: '1.2rem' }} />
                                ДОНАТ
                            </Box>
                        </Box>

                        {/* Spacer to push right elements away */}
                        <Box sx={{ flexGrow: 1 }} />

                        {/* Right part - language selector, user, logout */}
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                            {/* Language tab */}
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
                                        color: '#ffffff',
                                        backgroundColor: 'rgba(255, 255, 255, 0.04)',
                                    },
                                }}
                            >
                                {selectedLanguage ? getLanguageDisplay(selectedLanguage) : t('language')}
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
                                        {getLanguageDisplay(lang)}
                                    </MenuItem>
                                ))}
                            </Menu>
                            <ThemeToggle />
                            <Button sx={{ color: '#ffffff' }} onClick={handleUserInfo}>
                                {user?.username}
                            </Button>
                            <Button sx={{ color: '#ffffff' }} onClick={handleLogout}>
                                <IconButton color="inherit">
                                    <Logout/>
                                </IconButton>
                            </Button>
                        </Box>
                    </>
                )}
            </Toolbar>
            <Dialog open={dialogOpen} onClose={handleDialogClose}>
                <DialogTitle>{(t('userInfo'))}</DialogTitle>
                <DialogContent>
                    <Box display="flex" justifyContent="space-between" marginBottom={2}>
                        <React.Fragment>
                            <Box component="button" onClick={togglePasswordFields} sx={buttonLinkSx}>
                                {(t('changePassword'))}
                            </Box>
                        </React.Fragment>
                        <React.Fragment>
                            <Box component="button" onClick={handleDropSessions} sx={buttonLinkSx}>
                                {(t('dropSessions'))}
                            </Box>
                        </React.Fragment>
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
                                    {getLanguageDisplay(lang)}
                                </MenuItem>
                            ))}
                        </Select>
                    </FormControl>
                </DialogContent>
                <DialogActions>
                    <Button sx={{ color: 'text.primary' }} onClick={handleDialogClose}>{'Cancel'}</Button>
                    <Button sx={{ color: 'text.primary' }} onClick={handleUserChange}>{'Save'}</Button>
                </DialogActions>
            </Dialog>
            <DonateModal open={isDonateModalOpen} onClose={() => setIsDonateModalOpen(false)} />
        </AppBar>
    );
};

export default Header;
