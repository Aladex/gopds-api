import React, { useEffect, useRef, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HeartHandshake, LogOut } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';

import { useAuth } from '../../context/AuthContext';
import { useSearchBar } from '../../context/SearchBarContext';
import { getLanguageDisplaySafe, languageMapping } from '../../utils/languageUtils';
import { useMediaQuery } from '../hooks/useMediaQuery';
import { activeNavItem, useNavItems } from './navItems';
import DonateModal from './DonateModal';
import ThemeToggle from './ThemeToggle';

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
    const { logout, user, updateLang } = useAuth();
    const navigate = useNavigate();
    const location = useLocation();
    const { t } = useTranslation();
    const { languages, selectedLanguage, setSelectedLanguage } = useSearchBar();
    const [isDonateModalOpen, setIsDonateModalOpen] = useState(false);
    const [hiddenByScroll, setHiddenByScroll] = useState(false);
    const lastScrollYRef = useRef(0);

    const isMobile = useMediaQuery('(max-width: 600px)');
    const isVeryNarrow = useMediaQuery('(max-width: 354px)');

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

    // Only languages the interface knows how to name are offered.
    const supportedLanguages = languages.filter((lang) => getLanguageDisplaySafe(lang) !== null);

    /** A narrow bar has room for a flag and a code, but not a language's name. */
    const languageLabel = (lang: string) => {
        if (!isVeryNarrow) {
            return getLanguageDisplaySafe(lang);
        }
        const info = languageMapping[lang];
        return info ? `${info.flag} ${lang.toUpperCase()}` : lang.toUpperCase();
    };

    const chooseLanguage = (lang: string) => {
        updateLang(lang);
        setSelectedLanguage(lang);
    };

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

            // Near the top the bar always shows; below that it follows the
            // direction of travel, so reading down a list gives back the space.
            setHiddenByScroll(currentScrollY >= 50 && currentScrollY > previous);

            lastScrollYRef.current = currentScrollY;
        };

        window.addEventListener('scroll', handleScroll, { passive: true });
        return () => window.removeEventListener('scroll', handleScroll);
    }, [isMobile]);

    const donateButton = (
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

    const languageMenu = (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button
                    type="button"
                    className={cn(
                        'flex items-center justify-center truncate rounded px-2 font-medium uppercase text-neutral-400',
                        'hover:bg-white/5 hover:text-white',
                        isMobile ? 'h-8 min-w-[50px] text-[0.7rem]' : 'h-12 max-w-[120px] text-sm',
                    )}
                >
                    {selectedLanguage ? languageLabel(selectedLanguage) : t('language')}
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align={isMobile ? 'center' : 'start'}>
                {supportedLanguages.map((lang) => (
                    <DropdownMenuItem
                        key={lang}
                        onSelect={() => chooseLanguage(lang)}
                        className={cn(selectedLanguage === lang && 'bg-accent')}
                    >
                        {languageLabel(lang)}
                    </DropdownMenuItem>
                ))}
            </DropdownMenuContent>
        </DropdownMenu>
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
                        <Link to="/books/page/1" aria-label={t('booksTab')} className="flex items-center">
                            <img src="/logo.png" alt="" className="size-6" />
                        </Link>
                        <div className="flex items-center gap-0.5">
                            {donateButton}
                            {languageMenu}
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
                        <nav aria-label={t('booksTab')} className="flex items-center self-stretch">
                            {navItems.map((item) => (
                                <Link
                                    key={item.id}
                                    to={item.path}
                                    aria-current={current?.id === item.id ? 'page' : undefined}
                                    className={cn(
                                        'flex items-center gap-2 self-stretch border-b-2 px-4 text-sm font-medium uppercase tracking-wide',
                                        current?.id === item.id
                                            ? 'border-white text-white'
                                            : 'border-transparent text-neutral-400 hover:text-white',
                                    )}
                                >
                                    {item.id === 'books' && <img src="/logo.png" alt="" className="size-6" />}
                                    {item.label}
                                </Link>
                            ))}
                            <div className="ml-4 flex items-center">{donateButton}</div>
                        </nav>

                        <div className="flex-1" />

                        <div className="flex items-center gap-1">
                            {languageMenu}
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

            <DonateModal open={isDonateModalOpen} onClose={() => setIsDonateModalOpen(false)} />
        </header>
    );
};

export default Header;
