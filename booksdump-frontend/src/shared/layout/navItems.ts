import { useTranslation } from 'react-i18next';

/**
 * The application's top-level sections, in the order they are shown.
 *
 * The header and the bottom navigation are two presentations of the same list;
 * they used to keep a copy each, which is how they came to disagree about which
 * section was current.
 */

export interface NavItem {
    /** Stable key, independent of the translated label. */
    id: 'books' | 'collections' | 'opds' | 'admin';
    label: string;
    path: string;
    /** Matches every route belonging to the section, not just its landing page. */
    regex: RegExp;
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
        { id: 'opds', label: t('opdsTab'), path: '/catalog', regex: /^\/catalog/ },
    ];
    if (isSuperuser) {
        items.push({ id: 'admin', label: t('adminTab'), path: '/admin', regex: /^\/admin/ });
    }
    return items;
}

/** activeNavItem finds the section a path belongs to, or null outside them all. */
export function activeNavItem(items: NavItem[], pathname: string): NavItem | null {
    return items.find((item) => item.regex.test(pathname)) ?? null;
}
