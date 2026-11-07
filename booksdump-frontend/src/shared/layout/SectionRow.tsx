import React from 'react';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';
import { isPlainClick } from '@/shared/lib/isPlainClick';
import { useAuth } from '@/context/AuthContext';
import { useSearchBar } from '@/context/SearchBarContext';
import { searchPath } from '@/features/catalogue/addresses';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { useTravellingUnderline } from '@/shared/hooks/useTravellingUnderline';
import { PHONE_QUERY } from '@/shared/layout/breakpoints';
import NavUnderline from '@/shared/layout/NavUnderline';
import { activeNavItem, useNavItems, type NavItem } from '@/shared/layout/navItems';

/**
 * SectionRow is the one place the sections live, under the header, at every
 * width. On a phone it replaces the bar that stood fixed at the foot of the
 * screen and scrolls away with the page, so the list gets the height back.
 *
 * These navigate rather than switch panels, so they are links marked with
 * aria-current — not tabs, which would promise tabpanels that do not exist.
 *
 * Favourites is a section here and not a toggle, but what the toggle did with
 * the words in the box still holds: the filter changes, the search does not.
 */
const SectionRow: React.FC = () => {
    const { t } = useTranslation();
    const { pathname } = useLocation();
    const { user } = useAuth();
    const { searchItem, setSearchItem } = useSearchBar();
    const isPhone = useMediaQuery(PHONE_QUERY);

    const items = useNavItems(Boolean(user?.is_superuser));
    // A workspace rather than somewhere to read, and a phone reaches it through
    // the profile.
    const drawn = items.filter((item) => !(isPhone && item.id === 'admin'));

    // Matched against every section, drawn from this surface's own: inside the
    // admin panel on a phone none of the drawn ones is current, not the first.
    // The fallback stays for routes of no section — a search, an author's page
    // — where the reader did come from the catalogue and the row should say so.
    const current = activeNavItem(items, pathname) ?? items[0];

    const query = searchItem.trim();
    const hrefOf = (item: NavItem) => {
        if (item.id === 'favourites' && query) {
            return `${item.path}?title=${encodeURIComponent(query)}`;
        }
        // Back to the library: the words return to the broad search they would
        // have been had favourites never narrowed it.
        if (item.id === 'books' && current.id === 'favourites' && query) {
            return searchPath(query);
        }
        return item.path;
    };

    const { containerRef, setItemRef, box, placed } = useTravellingUnderline<HTMLAnchorElement>(
        current.id,
        drawn.map((item) => item.id + item.label).join('|'),
    );

    return (
        <div className="flex-none border-b border-border bg-card px-2 max-[359px]:px-1 min-[601px]:px-4">
            <nav
                ref={containerRef}
                aria-label={t('sectionsNav')}
                className="relative mx-auto flex h-10 max-w-[1200px] min-[601px]:h-11"
            >
                {drawn.map((item) => (
                    <Link
                        key={item.id}
                        to={hrefOf(item)}
                        ref={setItemRef(item.id)}
                        aria-current={current.id === item.id ? 'page' : undefined}
                        onClick={(event) => {
                            // Книги is the way out of a search. Left in the box,
                            // the words would read as one the list is not showing.
                            if (
                                item.id === 'books' &&
                                hrefOf(item) === item.path &&
                                isPlainClick(event)
                            ) {
                                setSearchItem('');
                            }
                        }}
                        className={cn(
                            'flex min-w-10 flex-none items-center justify-center whitespace-nowrap',
                            'px-[9px] text-sm max-[359px]:px-1.5 max-[359px]:text-[13px] min-[601px]:px-3',
                            'first:min-[601px]:-ml-3',
                            'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
                            'transition-colors duration-200 motion-reduce:transition-none',
                            current.id === item.id
                                ? 'font-bold text-foreground'
                                : 'text-muted-foreground hover:text-foreground',
                        )}
                    >
                        {item.label}
                    </Link>
                ))}
                <NavUnderline box={box} placed={placed} className="-mt-px h-[3px] bg-primary" />
            </nav>
        </div>
    );
};

export default SectionRow;
