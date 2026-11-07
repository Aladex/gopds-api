import React, { Suspense, useEffect, useRef, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { HeartHandshake, LogOut, ShieldUser, User } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/lib/utils';

import type * as systemApi from '@/api/system';
import { useAuth } from '@/context/AuthContext';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { activeNavItem, useNavItems } from '@/shared/layout/navItems';
import { takeLanding } from '@/shared/layout/landOnList';
/*
 * Loaded on demand. The dialog pulls in a QR code generator worth about twenty
 * kilobytes, and most readers never open it — there is no reason for everyone
 * to download it on first paint.
 */
const DonateModal = React.lazy(() => import('@/shared/layout/DonateModal'));
import ThemeToggle from '@/shared/layout/ThemeToggle';

type HeaderProps = {
    onOpenProfile?: () => void;
    /** Nothing to offer means no pill at all. */
    donateMethods: systemApi.DonateMethod[];
};

/**
 * Header is the application's top bar.
 *
 * The sections live in the row under it. On a phone all that stays up here is
 * the logo, the donate link, the theme and the way to the profile — and the bar
 * itself gets out of the way as the reader scrolls down a long list.
 */
const Header: React.FC<HeaderProps> = ({ onOpenProfile, donateMethods }) => {
    const { logout, user } = useAuth();
    const navigate = useNavigate();
    const location = useLocation();
    const { t } = useTranslation();
    const [isDonateModalOpen, setIsDonateModalOpen] = useState(false);
    const [hiddenByScroll, setHiddenByScroll] = useState(false);
    const lastScrollYRef = useRef(0);

    const isMobile = useMediaQuery('(max-width: 600px)');

    // Coming back to the narrow layout brings the bar back. Only the scroll
    // handler hides it, and it only runs while narrow, so a value left over from
    // an earlier narrow session would otherwise keep the bar hidden until the
    // reader happened to scroll.
    const [wasMobile, setWasMobile] = useState(isMobile);
    if (wasMobile !== isMobile) {
        setWasMobile(isMobile);
        setHiddenByScroll(false);
    }

    const allItems = useNavItems(Boolean(user?.is_superuser));
    const current = activeNavItem(allItems, location.pathname);
    /** Shown as an icon on a phone, where the header has no room for the row. */
    const adminItem = allItems.find((item) => item.id === 'admin');

    const handleLogout = () => {
        logout();
        navigate('/login');
    };

    useEffect(() => {
        if (!isMobile) {
            return;
        }

        const handleScroll = () => {
            const currentScrollY = window.scrollY;
            const previous = lastScrollYRef.current;
            lastScrollYRef.current = currentScrollY;

            // A turned page landing on its first card is not the reader going
            // down the list, and the bar stands over that card.
            if (takeLanding()) {
                setHiddenByScroll(false);
                return;
            }

            // Near the top the bar always shows; below that it follows the
            // direction of travel, so reading down a list gives back the space.
            setHiddenByScroll(currentScrollY >= 50 && currentScrollY > previous);
        };

        window.addEventListener('scroll', handleScroll, { passive: true });
        return () => window.removeEventListener('scroll', handleScroll);
    }, [isMobile]);

    const donateButton =
        donateMethods.length === 0 ? null : (
            <button
                type="button"
                onClick={() => setIsDonateModalOpen(true)}
                className={cn(
                    /*
                      Filled rather than lettered, and a pill rather than a bar.
                      It sat in the same grey as the section links, at the same
                      height, immediately after them — so it read as a fourth
                      section while behaving like none of them: it opens a dialog
                      rather than going anywhere. The shape says which of the two
                      it is before the colour does.

                      No dark: variant, because the header has no light one. It
                      is the chrome colour under both themes — a dark band across a
                      light page or a dark one — so this is the same button
                      either way.
                    */
                    'flex items-center gap-1.5 rounded-full font-medium uppercase',
                    'bg-green-400 text-green-950 hover:bg-green-300',
                    'focus-visible:ring-2 focus-visible:ring-green-300 focus-visible:ring-offset-2',
                    'focus-visible:ring-offset-chrome focus-visible:outline-none',
                    isMobile ? 'h-8 px-2.5 text-[0.7rem]' : 'h-9 px-3 text-sm',
                )}
            >
                <HeartHandshake className={isMobile ? 'size-4' : 'size-5'} />
                {t('donate', 'Донат')}
            </button>
        );

    return (
        <header
            className={cn(
                'w-full bg-chrome text-chrome-foreground',
                isMobile
                    ? 'fixed inset-x-0 top-0 z-chrome-top transition-transform duration-300'
                    : 'static',
                isMobile && hiddenByScroll && '-translate-y-full',
            )}
        >
            <div className={cn('flex items-center px-4', isMobile ? 'h-12' : 'h-16')}>
                {isMobile ? (
                    <div className="flex w-full items-center justify-between">
                        <Link
                            to="/books/page/1"
                            aria-label={t('booksTab')}
                            className="flex items-center"
                        >
                            <img src="/logo.png" alt="" className="size-6" />
                        </Link>
                        <div className="flex items-center gap-0.5">
                            {donateButton}
                            {/*
                              The phone's row leaves this section out, so it needs
                              saying here or it cannot be reached from a phone at
                              all.

                              Taken from the same list rather than gated on
                              is_superuser again: it is in that list only for a
                              superuser, so who sees it stays decided in one
                              place.
                            */}
                            {adminItem && (
                                <Link
                                    to={adminItem.path}
                                    aria-label={adminItem.label}
                                    title={adminItem.label}
                                    aria-current={current?.id === 'admin' ? 'page' : undefined}
                                    className={cn(
                                        'flex size-8 items-center justify-center rounded',
                                        current?.id === 'admin'
                                            ? 'text-chrome-foreground'
                                            : 'text-chrome-muted hover:text-chrome-foreground',
                                    )}
                                >
                                    <ShieldUser className="size-5" />
                                </Link>
                            )}
                            <ThemeToggle />
                            <button
                                type="button"
                                onClick={() => onOpenProfile?.()}
                                aria-label={t('user')}
                                title={t('user')}
                                className="flex size-10 items-center justify-center rounded text-chrome-foreground outline-none focus-visible:ring-[3px] focus-visible:ring-chrome-muted"
                            >
                                <User className="size-5" />
                            </button>
                        </div>
                    </div>
                ) : (
                    <>
                        <Link
                            to="/books/page/1"
                            aria-label={t('booksTab')}
                            className="flex items-center"
                        >
                            <img src="/logo.png" alt="" className="size-7" />
                        </Link>
                        <div className="ml-4 flex items-center">{donateButton}</div>

                        <div className="flex-1" />

                        <div className="flex items-center gap-1">
                            <ThemeToggle />
                            <Button
                                variant="ghost"
                                onClick={() => onOpenProfile?.()}
                                className="text-chrome-foreground hover:bg-chrome-foreground/10 hover:text-chrome-foreground focus-visible:ring-chrome-muted"
                            >
                                {user?.username}
                            </Button>
                            <Button
                                variant="ghost"
                                size="icon"
                                onClick={handleLogout}
                                title={t('logout', 'Выход')}
                                aria-label={t('logout', 'Выход')}
                                className="text-chrome-foreground hover:bg-chrome-foreground/10 hover:text-chrome-foreground focus-visible:ring-chrome-muted"
                            >
                                <LogOut className="size-5" />
                            </Button>
                        </div>
                    </>
                )}
            </div>

            {isDonateModalOpen && (
                <Suspense fallback={null}>
                    <DonateModal
                        open
                        methods={donateMethods}
                        onClose={() => setIsDonateModalOpen(false)}
                    />
                </Suspense>
            )}
        </header>
    );
};

export default Header;
