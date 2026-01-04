// src/components/PrivateRoute.tsx
import React, { memo, useCallback, useState } from 'react';
import { Navigate } from 'react-router-dom';
import { Box, useMediaQuery } from '@mui/material';
import { useAuth } from '../context/AuthContext';
import Header from './common/Header';
import Footer from "./common/Footer";
import BottomNavigation from './common/BottomNavigation';
import ProfileDrawer from './common/ProfileDrawer';
import LoadingSpinner from './common/LoadingSpinner';

const PrivateRoute: React.FC<{ children: React.ReactNode, requireSuperuser?: boolean }> = memo(({ children, requireSuperuser = false }) => {
    const { isAuthenticated, user, isLoaded, isLoading } = useAuth();
    const isMobile = useMediaQuery('(max-width:600px)');
    const [isProfileOpen, setIsProfileOpen] = useState(false);

    const handleOpenProfile = useCallback(() => {
        setIsProfileOpen(true);
    }, []);

    const handleCloseProfile = useCallback(() => {
        setIsProfileOpen(false);
    }, []);

    // Show loading spinner while checking authentication
    if (!isLoaded || isLoading) {
        return <LoadingSpinner message="loading" />;
    }

    if (!isAuthenticated) {
        return <Navigate to="/login" />;
    }

    if (requireSuperuser && !user?.is_superuser) {
        return <Navigate to="/404" />;
    }

    return (
        <>
            <Header onOpenProfile={handleOpenProfile} />
            <Box
                component="main"
                sx={{
                    minHeight: '100vh',
                    display: 'flex',
                    flexDirection: 'column',
                    paddingTop: isMobile ? '48px' : 0,
                    paddingBottom: isMobile ? '80px' : 0,
                }}
            >
                <Box sx={{ flex: 1 }}>
                    {children}
                </Box>
                {!isMobile && <Footer />}
            </Box>
            {isMobile && (
                <BottomNavigation isProfileOpen={isProfileOpen} onOpenProfile={handleOpenProfile} />
            )}
            <ProfileDrawer open={isProfileOpen} onClose={handleCloseProfile} />
        </>
    );
});

PrivateRoute.displayName = 'PrivateRoute';

export default PrivateRoute;
