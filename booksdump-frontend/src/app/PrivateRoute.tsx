// src/components/PrivateRoute.tsx
import React, { Suspense, memo, useEffect, useState } from 'react';
import { Navigate, useSearchParams } from 'react-router';

import { useAuth } from '@/context/AuthContext';
import Header from '@/shared/layout/Header';
import Footer from '@/shared/layout/Footer';
import SectionRow from '@/shared/layout/SectionRow';
import ProfileDrawer from '@/features/profile/ProfileDrawer';
import ProfileDialog from '@/features/profile/ProfileDialog';
import { ProfileOpenerContext, type ProfileSection } from '@/features/profile/ProfileOpenerContext';
import LoadingSpinner from '@/shared/components/LoadingSpinner';
import { CatalogueSearchProvider } from '@/features/catalogue/CatalogueSearchContext';
import LanguagePrompt from '@/features/catalogue/LanguagePrompt';
import { useDonateMethods } from '@/shared/hooks/useDonateMethods';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { PHONE_QUERY } from '@/shared/layout/breakpoints';

/**
 * Loaded on demand. The dialog pulls in a QR code generator worth about twenty
 * kilobytes, and most readers never open it — there is no reason for everyone
 * to download it on first paint.
 */
const DonateModal = React.lazy(() => import('@/shared/layout/DonateModal'));

const PrivateRoute: React.FC<{ children: React.ReactNode; requireSuperuser?: boolean }> = memo(
    ({ children, requireSuperuser = false }) => {
        const { isAuthenticated, user, isLoaded, isLoading } = useAuth();
        const isMobile = useMediaQuery(PHONE_QUERY);
        const donateMethods = useDonateMethods(isAuthenticated);
        const [isProfileDrawerOpen, setIsProfileDrawerOpen] = useState(false);
        const [isProfileDialogOpen, setIsProfileDialogOpen] = useState(false);
        // The header's pill and the footer's sentence open the same dialog.
        const [isDonateOpen, setIsDonateOpen] = useState(false);
        // Where the profile should open, when a page asked for a particular
        // part of it rather than the profile as such.
        const [profileSection, setProfileSection] = useState<ProfileSection | undefined>();

        // The profile is a sheet on a phone and a dialog on a desktop; which one
        // opens is decided here so both presentations share one trigger. Neither
        // is memoised: they are passed to click handlers, never to a dependency
        // list, and the React Compiler handles the rest.
        const handleOpenProfile = (section?: ProfileSection) => {
            setProfileSection(section);
            if (isMobile) {
                setIsProfileDrawerOpen(true);
            } else {
                setIsProfileDialogOpen(true);
            }
        };

        // The bot's «Open profile» button links to ?profile=telegram: the page
        // opens with that part of the profile, and the address is cleaned so
        // a reload does not open it again.
        const [searchParams, setSearchParams] = useSearchParams();
        const asked = searchParams.get('profile');
        useEffect(() => {
            if (!isAuthenticated || (asked !== 'telegram' && asked !== 'kindle')) return;
            setProfileSection(asked);
            if (isMobile) {
                setIsProfileDrawerOpen(true);
            } else {
                setIsProfileDialogOpen(true);
            }
            const rest = new URLSearchParams(searchParams);
            rest.delete('profile');
            setSearchParams(rest, { replace: true });
        }, [asked, isAuthenticated, isMobile, searchParams, setSearchParams]);

        const handleCloseProfile = () => {
            setIsProfileDrawerOpen(false);
            setIsProfileDialogOpen(false);
            setProfileSection(undefined);
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
            <ProfileOpenerContext.Provider value={{ openProfile: handleOpenProfile }}>
                {/* One search for the page: the field in the header, the chip
                    over the list and the takeover all read the same scope. */}
                <CatalogueSearchProvider>
                    <Header
                        onOpenProfile={() => handleOpenProfile()}
                        donateMethods={donateMethods}
                        onOpenDonate={() => setIsDonateOpen(true)}
                    />
                    <main className="flex w-full flex-col overflow-x-hidden">
                        <SectionRow />
                        <div className="flex-1">{children}</div>
                        <Footer
                            hasDonate={donateMethods.length > 0}
                            onOpenDonate={() => setIsDonateOpen(true)}
                        />
                    </main>
                </CatalogueSearchProvider>
                {isDonateOpen && (
                    <Suspense fallback={null}>
                        <DonateModal
                            open
                            methods={donateMethods}
                            onClose={() => setIsDonateOpen(false)}
                        />
                    </Suspense>
                )}
                <ProfileDrawer
                    open={isProfileDrawerOpen}
                    section={profileSection}
                    onClose={handleCloseProfile}
                />
                <ProfileDialog
                    open={isProfileDialogOpen}
                    section={profileSection}
                    onClose={handleCloseProfile}
                />
                {/* A new account has no books language, and no language means
                    no filter — so it belongs here, where both the reader and
                    the library's languages are known. */}
                <LanguagePrompt />
            </ProfileOpenerContext.Provider>
        );
    },
);

PrivateRoute.displayName = 'PrivateRoute';

export default PrivateRoute;
