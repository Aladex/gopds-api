import React, { Suspense, useEffect, useRef, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { HeartHandshake, LogOut } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/lib/utils';

import * as systemApi from '@/api/system';
import { useAuth } from '@/context/AuthContext';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { activeNavItem, useNavItems } from '@/shared/layout/navItems';
import { useTravellingUnderline } from '@/shared/hooks/useTravellingUnderline';
import NavUnderline from '@/shared/layout/NavUnderline';
/*
 * Loaded on demand. The dialog pulls in a QR code generator worth about twenty
 * kilobytes, and most readers never open it — there is no reason for everyone
 * to download it on first paint.
 */
const DonateModal = React.lazy(() => import('@/shared/layout/DonateModal'));
import ThemeToggle from '@/shared/layout/ThemeToggle';

type HeaderProps = {
    onOpenProfile?: () => void;
};

/**
 * Header is the application's top bar.
 *
 * It carries the section navigation on a desktop; on a phone that job belongs to
 * the bottom bar, so all that stays up here is the logo, the donate link, the
 * language and the theme — and the bar itself gets out of the way as the reader
 * scrolls down a long list.
 */
const Header: React.FC<HeaderProps> = ({ onOpenProfile }) => {
    const { logout, user } = useAuth();
    const navigate = useNavigate();
    const location = useLocation();
    const { t } = useTranslation();
    const [isDonateModalOpen, setIsDonateModalOpen] = useState(false);
    const [canDonate, setCanDonate] = useState(false);
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

    const navItems = useNavItems(Boolean(user?.is_superuser));
    const current = activeNavItem(navItems, location.pathname);

    // One bar that travels between the links; see the hook for why the row has
    // to be measured rather than styled. The shape covers a language change and
    // the admin link appearing for a superuser.
    const {
        containerRef: navRef,
        setItemRef,
        box: underline,
        placed,
    } = useTravellingUnderline<HTMLAnchorElement>(
        current?.id,
        navItems.map((item) => item.id + item.label).join('|') + String(isMobile),
    );

    const handleLogout = () => {
        logout();
        navigate('/login');
    };

    // Whether there is anything to offer at all. An installation that has
    // configured no methods should not show the button, rather than open an
    // empty dialog.
    useEffect(() => {
        let cancelled = false;
        systemApi
            .getDonateMethods()
            .then((methods) => {
                if (!cancelled) setCanDonate(methods.length > 0);
            })
            .catch(() => {
                // Nothing to offer if we cannot find out.
            });
        return () => {
            cancelled = true;
        };
    }, []);

    useEffect(() => {
        if (!isMobile) {
            return;
        }

        const handleScroll = () => {
            const currentScrollY = window.scrollY;
            const previous = lastScrollYRef.current;

            // Near the top the bar always shows; below that it follows the
            // direction of travel, so reading down a list gives back the space.
            setHiddenByScroll(currentScrollY >= 50 && currentScrollY > previous);

            lastScrollYRef.current = currentScrollY;
        };

        window.addEventListener('scroll', handleScroll, { passive: true });
        return () => window.removeEventListener('scroll', handleScroll);
    }, [isMobile]);

    const donateButton = !canDonate ? null : (
        <button
            type="button"
            onClick={() => setIsDonateModalOpen(true)}
            className={cn(
                'flex items-center gap-1 rounded px-2 font-medium uppercase text-neutral-400',
                'hover:bg-white/5 hover:text-white',
                isMobile ? 'h-8 text-[0.7rem]' : 'h-12 text-sm',
            )}
        >
            <HeartHandshake className={isMobile ? 'size-4' : 'size-5'} />
            {t('donate', 'Донат')}
        </button>
    );

    return (
        <header
            className={cn(
                'w-full bg-neutral-900 text-white',
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
                            <ThemeToggle />
                        </div>
                    </div>
                ) : (
                    <>
                        {/*
                          These navigate rather than switch panels, so they are
                          links marked with aria-current — not tabs, which would
                          promise tabpanels that do not exist.
                        */}
                        <nav
                            ref={navRef}
                            aria-label={t('booksTab')}
                            className="relative flex items-center self-stretch"
                        >
                            {navItems.map((item) => (
                                <Link
                                    key={item.id}
                                    to={item.path}
                                    ref={setItemRef(item.id)}
                                    aria-current={current?.id === item.id ? 'page' : undefined}
                                    className={cn(
                                        // The transparent border stays on every
                                        // link so the row keeps the height it
                                        // had when the border was the mark.
                                        'flex items-center gap-2 self-stretch border-b-2 border-transparent px-4',
                                        'text-sm font-medium uppercase tracking-wide',
                                        'transition-colors duration-200 motion-reduce:transition-none',
                                        current?.id === item.id
                                            ? 'text-white'
                                            : 'text-neutral-400 hover:text-white',
                                    )}
                                >
                                    {item.id === 'books' && (
                                        <img src="/logo.png" alt="" className="size-6" />
                                    )}
                                    {item.label}
                                </Link>
                            ))}
                            <NavUnderline box={underline} placed={placed} className="bg-white" />
                            <div className="ml-4 flex items-center">{donateButton}</div>
                        </nav>

                        <div className="flex-1" />

                        <div className="flex items-center gap-1">
                            <ThemeToggle />
                            <Button
                                variant="ghost"
                                onClick={() => onOpenProfile?.()}
                                className="text-white hover:bg-white/10 hover:text-white"
                            >
                                {user?.username}
                            </Button>
                            <Button
                                variant="ghost"
                                size="icon"
                                onClick={handleLogout}
                                title={t('logout', 'Выход')}
                                aria-label={t('logout', 'Выход')}
                                className="text-white hover:bg-white/10 hover:text-white"
                            >
                                <LogOut className="size-5" />
                            </Button>
                        </div>
                    </>
                )}
            </div>

            {isDonateModalOpen && (
                <Suspense fallback={null}>
                    <DonateModal open onClose={() => setIsDonateModalOpen(false)} />
                </Suspense>
            )}
        </header>
    );
};

export default Header;
