import React, { useEffect, useRef, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, HeartHandshake, LogOut, User } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/lib/utils';

import type * as systemApi from '@/api/system';
import { useAuth } from '@/context/AuthContext';
import { booksLanguageApplies } from '@/features/catalogue/addresses';
import { useCatalogueSearchContext } from '@/features/catalogue/CatalogueSearchContext';
import HeaderSearchField from '@/features/catalogue/HeaderSearchField';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import BookLanguage from '@/shared/layout/BookLanguage';
import { CONTENT_COLUMN, HEADER_WIDE_QUERY, PHONE_QUERY } from '@/shared/layout/breakpoints';
import { useBarLabel } from '@/shared/layout/useBarLabel';
import { useChromeScroll } from '@/shared/layout/useChromeScroll';
import ThemeToggle from '@/shared/layout/ThemeToggle';

type HeaderProps = {
    onOpenProfile?: () => void;
    onOpenDonate?: () => void;
    /** Nothing to offer means no pill at all. */
    donateMethods: systemApi.DonateMethod[];
};

const iconClass =
    'flex size-10 flex-none items-center justify-center rounded text-chrome-foreground outline-none focus-visible:ring-[3px] focus-visible:ring-chrome-muted';

/**
 * Header is the application's top bar. The sections live in the row under it.
 *
 * On a phone it is two storeys, one dark block. The bar holds the logo, the books
 * language, the donate pill and the profile, and goes with the page. The band
 * under it holds the field and docks at the top of the screen, and on the way
 * back up it brings the pill and the profile with it. Nothing green stands to the
 * right of the field in its band: it would read as the button that submits it.
 *
 * Focusing the field gives it the screen: the bar goes, pill included, the band
 * takes the top and the suggestions the rest. The input stays the same element
 * through all of it, or a phone would drop its keyboard.
 *
 * It is `display: contents` on a phone so that the band can stick to the page and
 * not to the header, whose box it would otherwise leave with.
 */
const Header: React.FC<HeaderProps> = ({ onOpenProfile, onOpenDonate, donateMethods }) => {
    const { logout, user } = useAuth();
    const navigate = useNavigate();
    const location = useLocation();
    const { t } = useTranslation();
    const { restoreWords } = useCatalogueSearchContext();
    const [taking, setTaking] = useState(false);

    const isPhone = useMediaQuery(PHONE_QUERY);
    const isWide = useMediaQuery(HEADER_WIDE_QUERY);
    const { hidden, docked } = useChromeScroll(isPhone);

    const barRef = useRef<HTMLDivElement | null>(null);
    const language = booksLanguageApplies(location.pathname);
    // The pill arrives after the first paint, with the methods, and takes room.
    const { label, group, rig } = useBarLabel(barRef, `${language}|${donateMethods.length}`);

    // Any arrival somewhere else ends the takeover: a Back from a phone's own
    // button goes round everything that would have closed it.
    const [seenKey, setSeenKey] = useState(location.key);
    if (seenKey !== location.key) {
        setSeenKey(location.key);
        setTaking(false);
    }

    // A rotation past the phone's width takes the band and its arrow away, so it
    // has to take the takeover and its lock with it.
    if (taking && !isPhone) {
        setTaking(false);
    }

    // The page stays where it was under the panel, and does not scroll with it.
    useEffect(() => {
        if (!taking) {
            return;
        }
        const { overflow } = document.body.style;
        document.body.style.overflow = 'hidden';
        return () => {
            document.body.style.overflow = overflow;
        };
    }, [taking]);

    const handleLogout = () => {
        logout();
        navigate('/login');
    };

    /** The arrow out: nothing typed there stands, and the page's own words return. */
    const closeTakeover = () => {
        setTaking(false);
        restoreWords();
    };

    /*
      Filled rather than lettered, and a pill rather than a bar. The shape says
      what it is before the colour does: it opens a dialog rather than going
      anywhere.

      No dark: variant, because the header has no light one. It is the chrome
      colour under both themes — a dark band across a light page or a dark one
      — so this is the same button either way.
    */
    const pillClass = 'rounded-full bg-green-400 font-medium text-green-950 uppercase';
    const phonePill = (inBand: boolean) =>
        donateMethods.length === 0 ? null : (
            <button
                type="button"
                onClick={onOpenDonate}
                className={cn(
                    'group flex h-10 flex-none items-center px-1 outline-none',
                    inBand && 'max-[359px]:px-0.5',
                )}
            >
                <span
                    className={cn(
                        pillClass,
                        'flex h-8 items-center gap-1.5 px-2.5 text-[0.7rem] group-hover:bg-green-300',
                        'group-focus-visible:ring-2 group-focus-visible:ring-green-300 group-focus-visible:ring-offset-2 group-focus-visible:ring-offset-chrome',
                    )}
                >
                    <HeartHandshake className={cn('size-4', inBand && 'max-[359px]:hidden')} />
                    {t('donate', 'Донат')}
                </span>
            </button>
        );

    const logo = (
        <Link to="/books/page/1" aria-label={t('booksTab')} className={iconClass}>
            <img src="/logo.png" alt="" className="size-6" />
        </Link>
    );

    const profile = (
        <button
            type="button"
            onClick={() => onOpenProfile?.()}
            aria-label={t('user')}
            title={t('user')}
            className={iconClass}
        >
            <User className="size-5" />
        </button>
    );

    if (isPhone) {
        const showing = docked && !taking;
        return (
            <header className="contents">
                <div
                    ref={barRef}
                    className={cn(
                        'flex h-10 flex-none items-center bg-chrome text-chrome-foreground',
                        taking && 'invisible',
                    )}
                >
                    {language ? (
                        logo
                    ) : (
                        <Link
                            to="/books/page/1"
                            aria-label={t('booksTab')}
                            className="flex h-10 flex-none items-center gap-1.5 rounded px-2 outline-none focus-visible:ring-[3px] focus-visible:ring-chrome-muted"
                        >
                            <img src="/logo.png" alt="" className="size-6" />
                            {/* The product's name, not a word to translate. */}
                            <span className="text-[17px] leading-none font-semibold tracking-[-0.01em]">
                                booksdump
                            </span>
                        </Link>
                    )}
                    {language && (
                        <span
                            ref={group}
                            className="relative flex flex-none items-center text-[13px] text-chrome-muted"
                        >
                            <span
                                ref={rig}
                                aria-hidden="true"
                                className="pointer-events-none invisible absolute flex whitespace-nowrap"
                            >
                                <span>{t('booksLanguageLabel')}</span>
                                <span>{t('booksLanguageLabelShort')}</span>
                            </span>
                            {label !== 'none' && (
                                <span className="px-px whitespace-nowrap">
                                    {t(
                                        label === 'long'
                                            ? 'booksLanguageLabel'
                                            : 'booksLanguageLabelShort',
                                    )}
                                </span>
                            )}
                            <BookLanguage where="bar" className="focus-visible:ring-chrome-muted" />
                        </span>
                    )}
                    <span data-bar-spring="" className="min-w-0 flex-1" />
                    {phonePill(false)}
                    {profile}
                </div>
                {/* Held open while the band has left the flow, so the page does
                    not move under the panel. */}
                {taking && <div aria-hidden="true" className="h-11 flex-none" />}
                <div
                    data-testid="search-band"
                    className={cn(
                        'z-chrome-top flex h-11 flex-none items-center bg-chrome text-chrome-foreground',
                        'transition-transform duration-300 motion-reduce:transition-none',
                        taking ? 'fixed inset-x-0 top-0 pr-2' : 'sticky top-0',
                        !taking && !showing && 'px-2',
                        hidden && !taking && '-translate-y-full',
                    )}
                >
                    {taking && (
                        <button
                            type="button"
                            onClick={closeTakeover}
                            aria-label={t('search.close')}
                            className={iconClass}
                        >
                            <ArrowLeft className="size-5" />
                        </button>
                    )}
                    {showing && logo}
                    {showing && phonePill(true)}
                    <HeaderSearchField
                        className="relative min-w-0 flex-1"
                        taking={taking}
                        onTake={() => setTaking(true)}
                        onDone={() => setTaking(false)}
                    />
                    {showing && profile}
                </div>
            </header>
        );
    }

    const desktopPill =
        donateMethods.length === 0 ? null : (
            <button
                type="button"
                onClick={onOpenDonate}
                className={cn(
                    pillClass,
                    'flex h-9 flex-none items-center gap-1.5 px-3 text-sm hover:bg-green-300',
                    'focus-visible:ring-2 focus-visible:ring-green-300 focus-visible:ring-offset-2',
                    'focus-visible:ring-offset-chrome focus-visible:outline-none',
                )}
            >
                <HeartHandshake className="size-5" />
                {t('donate', 'Донат')}
            </button>
        );

    return (
        <header className="w-full flex-none bg-chrome px-4 text-chrome-foreground">
            {/* The band runs the width of the screen; what is on it keeps to the
                list's column, or a wide monitor sets the logo and the way out a
                long way either side of the books. */}
            <div className={cn(CONTENT_COLUMN, 'flex h-16 items-center gap-2')}>
                <Link
                    to="/books/page/1"
                    aria-label={t('booksTab')}
                    className="flex flex-none items-center"
                >
                    <img src="/logo.png" alt="" className="size-7" />
                </Link>
                {/* Green beside a field reads as its submit, so the pill goes to
                    the left of it until the field stops short of the row's end. */}
                {!isWide && desktopPill}
                <HeaderSearchField className="relative max-w-[560px] min-w-0 flex-[1_1_560px]" />
                <div className="flex-1" />
                {isWide && desktopPill}
                <div className="flex flex-none items-center gap-1">
                    <ThemeToggle />
                    <Button
                        variant="ghost"
                        onClick={() => onOpenProfile?.()}
                        className="max-w-40 min-w-0 text-chrome-foreground hover:bg-chrome-foreground/10 hover:text-chrome-foreground focus-visible:ring-chrome-muted"
                    >
                        <span className="truncate">{user?.username}</span>
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
            </div>
        </header>
    );
};

export default Header;
