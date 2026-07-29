import React, { Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { HeartHandshake, LogOut } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/lib/utils';

import * as systemApi from '@/api/system';
import { useAuth } from '@/context/AuthContext';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { activeNavItem, useNavItems } from '@/shared/layout/navItems';
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

    /*
     * The underline is one bar that moves, not a border that appears under the
     * link being opened and vanishes from the last one. Switching sections was
     * an instant swap of two borders, which reads as a flicker between two
     * places rather than as travel between them.
     *
     * It has to be measured: the links are sized by their words, and the words
     * change with the interface language. A ResizeObserver on the row catches
     * that, along with the browser settling on its fonts and the admin link
     * appearing for a superuser.
     */
    const navRef = useRef<HTMLElement | null>(null);
    const linkRefs = useRef(new Map<string, HTMLAnchorElement>());
    const [underline, setUnderline] = useState<{ left: number; width: number } | null>(null);
    // There is nothing to travel from on the first measurement, so the bar is
    // placed rather than slid — otherwise it flies in from the left on load.
    const [placed, setPlaced] = useState(false);

    // useNavItems rebuilds the array every render by design, so the effect keys
    // off what would actually move the bar rather than off the array's identity.
    const navShape = navItems.map((item) => item.id + item.label).join('|');

    const measureUnderline = useCallback(() => {
        const nav = navRef.current;
        const link = current ? linkRefs.current.get(current.id) : undefined;
        if (!nav || !link) {
            setUnderline(null);
            return;
        }
        const navBox = nav.getBoundingClientRect();
        const linkBox = link.getBoundingClientRect();
        const next = { left: linkBox.left - navBox.left, width: linkBox.width };
        // A fresh object every render would loop, since the effect below runs
        // on values derived from this state.
        setUnderline((prev) =>
            prev && prev.left === next.left && prev.width === next.width ? prev : next,
        );
        setPlaced(true);
    }, [current]);

    useLayoutEffect(() => {
        const nav = navRef.current;
        if (!nav) {
            return;
        }
        measureUnderline();
        const observer = new ResizeObserver(measureUnderline);
        observer.observe(nav);
        return () => observer.disconnect();
        // navShape stands in for the link list, which is a new array on every
        // render; depending on the array itself would re-run this each time.
    }, [measureUnderline, navShape, isMobile]);

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
                                    ref={(node) => {
                                        if (node) {
                                            linkRefs.current.set(item.id, node);
                                        } else {
                                            linkRefs.current.delete(item.id);
                                        }
                                    }}
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
                            {/* Decorative: which link is current is already said
                                by aria-current on the link itself. */}
                            <span
                                aria-hidden
                                className={cn(
                                    'pointer-events-none absolute bottom-0 h-0.5 bg-white',
                                    placed &&
                                        'transition-[left,width,opacity] duration-300 ease-out motion-reduce:transition-none',
                                    underline ? 'opacity-100' : 'opacity-0',
                                )}
                                style={{
                                    left: underline?.left ?? 0,
                                    width: underline?.width ?? 0,
                                }}
                            />
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
