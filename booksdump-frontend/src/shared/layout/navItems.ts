import { useTranslation } from 'react-i18next';

/**
 * The application's top-level sections, in the order they are shown.
 *
 * The header and the bottom navigation are two presentations of the same list;
 * they used to keep a copy each, which is how they came to disagree about which
 * section was current.
 *
 * They are allowed to show different subsets of it, but only by saying so here.
 * A wide header has room for a section a reader visits now and then; the bar at
 * the foot of a phone has four places at most, and every one of them costs the
 * others width.
 */

/** Where a section is offered. */
export type NavSurface = 'header' | 'bottom';

export interface NavItem {
    /** Stable key, independent of the translated label. */
    id: 'books' | 'collections' | 'admin';
    label: string;
    path: string;
    /** Matches every route belonging to the section, not just its landing page. */
    regex: RegExp;
    /**
     * The surfaces this section appears on. Absent means both.
     *
     * Whichever surface leaves a section out still has to recognise its routes,
     * or it would mark the wrong one as current — so this filters what is drawn,
     * never what is matched.
     */
    surfaces?: NavSurface[];
}

/**
 * Built afresh on every render rather than memoised. It is four small objects,
 * nobody puts the array in a dependency list, and a memo would have needed
 * i18n.language named by hand so the labels followed a language change —
 * a dependency the linter could not see and had to be told to ignore.
 */
export function useNavItems(isSuperuser: boolean): NavItem[] {
    const { t } = useTranslation();

    const items: NavItem[] = [
        { id: 'books', label: t('booksTab'), path: '/books/page/1', regex: /^\/books\/page\/\d+/ },
        {
            id: 'collections',
            label: t('collectionsTab', 'Подборки'),
            path: '/collections',
            regex: /^\/collections/,
        },
    ];
    if (isSuperuser) {
        items.push({
            id: 'admin',
            label: t('adminTab'),
            path: '/admin',
            // A workspace rather than somewhere to read, belonging to one
            // person, who does not go there from a phone. The header has room
            // for it; the bar at the foot of a phone is worth more than that.
            surfaces: ['header'],
            regex: /^\/admin/,
        });
    }
    return items;
}

/** activeNavItem finds the section a path belongs to, or null outside them all. */
export function activeNavItem(items: NavItem[], pathname: string): NavItem | null {
    return items.find((item) => item.regex.test(pathname)) ?? null;
}

/** onSurface is the subset of sections a given surface offers. */
export function onSurface(items: NavItem[], surface: NavSurface): NavItem[] {
    return items.filter((item) => !item.surfaces || item.surfaces.includes(surface));
}
