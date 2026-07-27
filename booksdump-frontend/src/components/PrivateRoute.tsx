// src/components/PrivateRoute.tsx
import React, { memo, useState } from 'react';
import { Navigate } from 'react-router-dom';

import { cn } from '@/lib/utils';

import { useAuth } from '../context/AuthContext';
import Header from './common/Header';
import Footer from './common/Footer';
import BottomNavigation from './common/BottomNavigation';
import ProfileDrawer from './common/ProfileDrawer';
import ProfileDialog from './common/ProfileDialog';
import LoadingSpinner from './common/LoadingSpinner';
import { useMediaQuery } from './hooks/useMediaQuery';

const PrivateRoute: React.FC<{ children: React.ReactNode; requireSuperuser?: boolean }> = memo(
    ({ children, requireSuperuser = false }) => {
        const { isAuthenticated, user, isLoaded, isLoading } = useAuth();
        const isMobile = useMediaQuery('(max-width: 600px)');
        const [isProfileDrawerOpen, setIsProfileDrawerOpen] = useState(false);
        const [isProfileDialogOpen, setIsProfileDialogOpen] = useState(false);

        // The profile is a sheet on a phone and a dialog on a desktop; which one
        // opens is decided here so both presentations share one trigger. Neither
        // is memoised: they are passed to click handlers, never to a dependency
        // list, and the React Compiler handles the rest.
        const handleOpenProfile = () => {
            if (isMobile) {
                setIsProfileDrawerOpen(true);
            } else {
                setIsProfileDialogOpen(true);
            }
        };

        const handleCloseProfile = () => {
            setIsProfileDrawerOpen(false);
            setIsProfileDialogOpen(false);
        };

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
                {/* The bars are fixed on a phone, so the page is padded clear of
                    them rather than sliding underneath. */}
                <main
                    className={cn(
                        'flex min-h-screen w-full flex-col overflow-x-hidden',
                        isMobile && 'pt-12 pb-20',
                    )}
                >
                    <div className="flex-1">{children}</div>
                    {!isMobile && <Footer />}
                </main>
                {isMobile && (
                    <BottomNavigation
                        isProfileOpen={isProfileDrawerOpen}
                        onOpenProfile={handleOpenProfile}
                    />
                )}
                <ProfileDrawer open={isProfileDrawerOpen} onClose={handleCloseProfile} />
                <ProfileDialog open={isProfileDialogOpen} onClose={handleCloseProfile} />
            </>
        );
    },
);

PrivateRoute.displayName = 'PrivateRoute';

export default PrivateRoute;
