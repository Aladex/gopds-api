// src/components/PrivateRoute.tsx
import React, { memo, useCallback, useState } from 'react';
import { Navigate } from 'react-router-dom';
import { Box, useMediaQuery } from '@mui/material';
import { useAuth } from '../context/AuthContext';
import Header from './common/Header';
import Footer from "./common/Footer";
import BottomNavigation from './common/BottomNavigation';
import ProfileDrawer from './common/ProfileDrawer';
import ProfileDialog from './common/ProfileDialog';
import LoadingSpinner from './common/LoadingSpinner';

const PrivateRoute: React.FC<{ children: React.ReactNode, requireSuperuser?: boolean }> = memo(({ children, requireSuperuser = false }) => {
    const { isAuthenticated, user, isLoaded, isLoading } = useAuth();
    const isMobile = useMediaQuery('(max-width:600px)');
    const [isProfileDrawerOpen, setIsProfileDrawerOpen] = useState(false);
    const [isProfileDialogOpen, setIsProfileDialogOpen] = useState(false);

    const handleOpenProfile = useCallback(() => {
        if (isMobile) {
            setIsProfileDrawerOpen(true);
        } else {
            setIsProfileDialogOpen(true);
        }
    }, [isMobile]);

    const handleCloseProfile = useCallback(() => {
        setIsProfileDrawerOpen(false);
        setIsProfileDialogOpen(false);
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
                    width: '100%',
                    overflowX: 'hidden',
                }}
            >
                <Box sx={{ flex: 1 }}>
                    {children}
                </Box>
                {!isMobile && <Footer />}
            </Box>
            {isMobile && (
                <BottomNavigation isProfileOpen={isProfileDrawerOpen} onOpenProfile={handleOpenProfile} />
            )}
            <ProfileDrawer open={isProfileDrawerOpen} onClose={handleCloseProfile} />
            <ProfileDialog open={isProfileDialogOpen} onClose={handleCloseProfile} />
        </>
    );
});

PrivateRoute.displayName = 'PrivateRoute';

export default PrivateRoute;
