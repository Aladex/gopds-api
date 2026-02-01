import React, { useEffect, useMemo, useState } from 'react';
import { BottomNavigation as MuiBottomNavigation, BottomNavigationAction, Paper } from '@mui/material';
import { AdminPanelSettings, LibraryBooks, MenuBook, Person } from '@mui/icons-material';
import { useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../../context/AuthContext';

type BottomNavigationProps = {
    isProfileOpen: boolean;
    onOpenProfile: () => void;
};

const BottomNavigation: React.FC<BottomNavigationProps> = ({ isProfileOpen, onOpenProfile }) => {
    const navigate = useNavigate();
    const location = useLocation();
    const { t } = useTranslation();
    const { user } = useAuth();
    const [value, setValue] = useState('books');

    const navItems = useMemo(() => {
        const items = [
            { value: 'books', label: t('booksTab'), icon: <MenuBook />, path: '/books/page/1', regex: /^\/books\/page\/\d+/ },
            { value: 'opds', label: t('opdsTab'), icon: <LibraryBooks />, path: '/catalog', regex: /^\/catalog/ },
        ];

        if (user?.is_superuser) {
            items.push({ value: 'admin', label: t('adminTab'), icon: <AdminPanelSettings />, path: '/admin', regex: /^\/admin/ });
        }

        return items;
    }, [t, user?.is_superuser]);

    useEffect(() => {
        if (isProfileOpen) {
            setValue('profile');
            return;
        }

        const match = navItems.find((item) => item.regex.test(location.pathname));
        if (match) {
            setValue(match.value);
        } else {
            setValue('books');
        }
    }, [isProfileOpen, location.pathname, navItems]);

    const handleChange = (_event: React.SyntheticEvent, newValue: string) => {
        if (newValue === 'profile') {
            onOpenProfile();
            setValue('profile');
            return;
        }

        const target = navItems.find((item) => item.value === newValue);
        if (target) {
            navigate(target.path);
        }
    };

    return (
        <Paper sx={{ position: 'fixed', bottom: 0, left: 0, right: 0, zIndex: 1100, bgcolor: '#2f2f2f' }} elevation={8}>
            <MuiBottomNavigation
                value={value}
                onChange={handleChange}
                showLabels
                sx={{
                    '& .MuiBottomNavigationAction-root': {
                        minWidth: 0,
                    },
                }}
            >
                {navItems.map((item) => (
                    <BottomNavigationAction
                        key={item.value}
                        value={item.value}
                        label={item.label}
                        icon={item.icon}
                        aria-label={item.label}
                    />
                ))}
                <BottomNavigationAction
                    value="profile"
                    label={t('user')}
                    icon={<Person />}
                    aria-label={t('user')}
                />
            </MuiBottomNavigation>
        </Paper>
    );
};

export default BottomNavigation;
