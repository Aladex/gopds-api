import React, { useLayoutEffect } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';
import { formatCount } from '@/shared/lib/formatCount';
import { isPlainClick } from '@/shared/lib/isPlainClick';
import { useAuth } from '@/context/AuthContext';
import { useSearchBar } from '@/context/SearchBarContext';
import { isSearchPath, searchPath } from '@/features/catalogue/addresses';
import { useSearchCounts } from '@/features/catalogue/hooks/useSearchCounts';
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
/**
 * How far a row of counts gives way on a narrow phone: tighter padding and no
 * divider, then the other tab's count. Tried in order against the row itself, in
 * one pass, so none of it is ever painted; only the width of the column can
 * change the answer, and the row's own content cannot wake it.
 */
const SQUEEZE_STEPS = ['0', '1', '2'];

function fitRow(row: HTMLElement) {
    for (const step of SQUEEZE_STEPS) {
        row.dataset.squeeze = step;
        if (row.scrollWidth <= row.clientWidth) {
            return;
        }
    }
}

const tabClass = (active: boolean) =>
    cn(
        'flex min-w-10 flex-none items-center justify-center gap-[5px] whitespace-nowrap',
        'px-[9px] text-sm max-[359px]:px-1.5 max-[359px]:text-[13px] min-[601px]:px-3',
        'group-data-[squeeze=1]/row:px-1 group-data-[squeeze=2]/row:px-1',
        'first:min-[601px]:-ml-3',
        'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
        'transition-colors duration-200 motion-reduce:transition-none',
        active ? 'font-bold text-foreground' : 'text-muted-foreground hover:text-foreground',
    );

const SectionRow: React.FC = () => {
    const { t, i18n } = useTranslation();
    const { pathname } = useLocation();
    const [params] = useSearchParams();
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

    // A search has two answers, and they take the first places in the row. The
    // words are the search's own, from the address: the box may hold a draft.
    const asked = isSearchPath(pathname) ? (params.get('q')?.trim() ?? '') : '';
    const onAuthors = params.get('type') === 'authors';
    const counts = useSearchCounts(asked, user?.books_lang || '');
    const tabs = [
        { id: 'found-books', label: t('booksTab'), type: 'books', count: counts.books },
        { id: 'found-authors', label: t('authorsTab'), type: 'authors', count: counts.authors },
    ] as const;
    const currentKey = asked ? (onAuthors ? 'found-authors' : 'found-books') : current.id;

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

    const shape = [
        ...drawn.map((item) => item.id + item.label),
        asked,
        counts.books,
        counts.authors,
    ].join('|');
    const { containerRef, setItemRef, box, placed } = useTravellingUnderline<HTMLAnchorElement>(
        currentKey,
        shape,
    );

    useLayoutEffect(() => {
        const row = containerRef.current;
        if (!row) {
            return;
        }
        fitRow(row);
        if (typeof ResizeObserver === 'undefined') {
            return;
        }
        const observer = new ResizeObserver(() => fitRow(row));
        observer.observe(row);
        return () => observer.disconnect();
    }, [containerRef, shape]);

    // On a phone the row of a search has room for the answers and favourites.
    const rest = asked
        ? drawn.filter((item) => item.id !== 'books' && (!isPhone || item.id === 'favourites'))
        : drawn;

    return (
        <div className="flex-none border-b border-border bg-card px-2 max-[359px]:px-1 min-[601px]:px-4">
            <nav
                ref={containerRef}
                aria-label={t('sectionsNav')}
                className="group/row relative mx-auto flex h-10 max-w-[1200px] min-[601px]:h-11"
            >
                {asked &&
                    tabs.map((tab) => (
                        <Link
                            key={tab.id}
                            to={searchPath(asked, tab.type)}
                            ref={setItemRef(tab.id)}
                            aria-current={currentKey === tab.id ? 'page' : undefined}
                            className={tabClass(currentKey === tab.id)}
                        >
                            {tab.label}
                            {tab.count !== undefined && (
                                <span
                                    className={cn(
                                        'text-xs tabular-nums',
                                        currentKey === tab.id
                                            ? 'font-medium'
                                            : 'font-normal text-muted-foreground group-data-[squeeze=2]/row:hidden',
                                    )}
                                >
                                    {formatCount(tab.count, i18n?.language ?? 'ru')}
                                </span>
                            )}
                        </Link>
                    ))}
                {asked && (
                    <>
                        <span className="min-w-0.5 flex-1" />
                        <span
                            aria-hidden="true"
                            className="mx-[3px] h-[18px] w-px flex-none self-center bg-input group-data-[squeeze=1]/row:hidden group-data-[squeeze=2]/row:hidden"
                        />
                    </>
                )}
                {rest.map((item) => (
                    <Link
                        key={item.id}
                        to={hrefOf(item)}
                        ref={setItemRef(item.id)}
                        aria-current={currentKey === item.id ? 'page' : undefined}
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
                        className={tabClass(currentKey === item.id)}
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
