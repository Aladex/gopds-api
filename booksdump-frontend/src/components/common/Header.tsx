import React, {useMemo, useEffect, useRef, useState} from 'react';
import {useNavigate} from 'react-router-dom';
import {
    AppBar,
    Box,
    Button,
    IconButton,
    Tab,
    Tabs,
    Toolbar,
    useMediaQuery,
    MenuItem,
    Menu
} from '@mui/material';
import {useAuth} from '../../context/AuthContext';
import {useTranslation} from 'react-i18next';
import {Logout, VolunteerActivism} from "@mui/icons-material";
import { useSearchBar } from "../../context/SearchBarContext";
import { getLanguageDisplaySafe, languageMapping } from "../../utils/languageUtils";
import DonateModal from "./DonateModal";
import ThemeToggle from "./ThemeToggle";

type HeaderProps = {
    onOpenProfile?: () => void;
};

const Header: React.FC<HeaderProps> = ({ onOpenProfile }) => {
    const {logout, user, updateLang} = useAuth();
    const navigate = useNavigate();
    const {t, i18n} = useTranslation();
    const [value, setValue] = useState(0);
    const isMobile = useMediaQuery('(max-width:600px)');
    const isVeryNarrow = useMediaQuery('(max-width:354px)'); // Check for very narrow screens
    const { languages, selectedLanguage, setSelectedLanguage } = useSearchBar();
    const [languageMenuAnchor, setLanguageMenuAnchor] = useState<null | HTMLElement>(null);
    const [isDonateModalOpen, setIsDonateModalOpen] = useState<boolean>(false);
    const [isHeaderVisible, setIsHeaderVisible] = useState(true);
    const lastScrollYRef = useRef(0);


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

    const handleLogout = () => {
        logout();
        navigate('/login');
    };

    useEffect(() => {
        if (!isMobile) {
            setIsHeaderVisible(true);
            return;
        }

        const handleScroll = () => {
            const currentScrollY = window.scrollY;
            const prevScrollY = lastScrollYRef.current;

            if (currentScrollY < 50) {
                setIsHeaderVisible(true);
            } else if (currentScrollY > prevScrollY) {
                setIsHeaderVisible(false);
            } else {
                setIsHeaderVisible(true);
            }

            lastScrollYRef.current = currentScrollY;
        };

        window.addEventListener('scroll', handleScroll, { passive: true });
        return () => window.removeEventListener('scroll', handleScroll);
    }, [isMobile]);

    useEffect(() => {
        const currentPath = window.location.pathname;
        const currentTab = menuItems.findIndex(item => item.regex.test(currentPath));
        if (currentTab !== -1) {
            setValue(currentTab);
        }
    }, [menuItems]);

    const handleChange = (event: React.SyntheticEvent, newValue: number) => {
        setValue(newValue);
    };

    const a11yProps = (index: number) => {
        return {
            id: `simple-tab-${index}`,
            'aria-controls': `simple-tabpanel-${index}`,
        };
    };

    const handleTabClick = (index: number, path: string) => {
        setValue(index);
        navigate(path);
    };

    return (
        <AppBar
            position={isMobile ? 'fixed' : 'static'}
            sx={{
                bgcolor: '#2f2f2f',
                color: '#ffffff',
                backgroundImage: 'none',
                transform: isMobile && !isHeaderVisible ? 'translateY(-100%)' : 'translateY(0)',
                transition: isMobile ? 'transform 0.3s ease-in-out' : 'none',
                zIndex: isMobile ? 1200 : 'auto',
            }}
            style={{ backgroundColor: '#2f2f2f', color: '#ffffff', backgroundImage: 'none' }}
        >
            <Toolbar sx={{ minHeight: isMobile ? 48 : 64 }}>
                {isMobile ? (
                    <>
                        <Box display="flex" alignItems="center" justifyContent="space-between" width="100%">
                            <Box
                                onClick={() => navigate('/books/page/1')}
                                sx={{ display: 'flex', alignItems: 'center', cursor: 'pointer' }}
                            >
                                <img src="/logo.png" alt="Logo" style={{ width: 24, height: 24 }} />
                            </Box>
                            <Box display="flex" alignItems="center" gap={0.5}>
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
                            </Box>
                        </Box>
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
                            <Button sx={{ color: '#ffffff' }} onClick={() => onOpenProfile?.()}>
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
            <DonateModal open={isDonateModalOpen} onClose={() => setIsDonateModalOpen(false)} />
        </AppBar>
    );
};

export default Header;
