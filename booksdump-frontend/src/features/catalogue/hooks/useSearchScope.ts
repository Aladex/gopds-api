import { useEffect, useRef, useState } from 'react';
import { useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';

import * as booksApi from '@/api/books';
import { useAuthor } from '@/context/AuthorContext';

export type SearchScopeKind = 'author' | 'series' | 'genre' | 'collection' | 'favorites';

export interface SearchScope {
    /** Which list the search is confined to; null away from any scoped route. */
    kind: SearchScopeKind | null;
    /** The scoped entity's id; empty for favourites, which belong to the reader. */
    id: string;
    /** Whether the next search will be confined to the scope. */
    active: boolean;
    /** Where a scoped search lands: page one of the list in front of the reader. */
    firstPagePath: string;
    /** The chip's text, already localized. */
    label: string;
    /** Turns the scope off — the reader asking to search the whole library. */
    release: () => void;
    /** Turns the scope back on after it was released. */
    reclaim: () => void;
}

/**
 * Called on arriving in a scoped list, so the panel can put itself in the
 * mode the scope belongs to. A reader who got here by searching for a name is
 * still in "authors by name", and a scope over that means nothing — their
 * next search is almost certainly among the books in front of them, so the
 * panel switches to titles and lets the scope apply.
 */
type OnEnter = () => void;

interface RouteScope {
    kind: SearchScopeKind;
    id: string;
    firstPagePath: string;
}

// The scope is read from the route rather than held in context, so it
// survives a reload and a pasted URL. SearchBar lives outside <Routes> and
// cannot use useParams, hence plain pathname matching.
const SCOPED_ROUTES: Array<{ match: RegExp; build: (id: string) => RouteScope }> = [
    {
        match: /^\/books\/find\/author\/(\d+)(?:\/|$)/,
        build: (id) => ({ kind: 'author', id, firstPagePath: `/books/find/author/${id}/1` }),
    },
    {
        match: /^\/books\/find\/category\/(\d+)(?:\/|$)/,
        build: (id) => ({ kind: 'series', id, firstPagePath: `/books/find/category/${id}/1` }),
    },
    {
        match: /^\/books\/find\/genre\/(\d+)(?:\/|$)/,
        build: (id) => ({ kind: 'genre', id, firstPagePath: `/books/find/genre/${id}/1` }),
    },
    {
        match: /^\/collections\/(\d+)\/page(?:\/|$)/,
        build: (id) => ({ kind: 'collection', id, firstPagePath: `/collections/${id}/page/1` }),
    },
    {
        match: /^\/books\/favorite(?:\/|$)/,
        build: () => ({ kind: 'favorites', id: '', firstPagePath: '/books/favorite/1' }),
    },
];

const routeScopeFrom = (pathname: string): RouteScope | null => {
    for (const { match, build } of SCOPED_ROUTES) {
        const found = pathname.match(match);
        if (found) {
            return build(found[1] ?? '');
        }
    }
    return null;
};

/**
 * searchTitleFromLocation recovers the visible query from where a search put
 * it: the segment of a title route, or the title parameter a scoped search
 * writes next to its filters. A cold reload seeds the input from this rather
 * than showing a blank field over a filtered list.
 */
export const searchTitleFromLocation = (pathname: string, search: string): string => {
    const segments = pathname.split('/');
    // ['', 'books', 'find', 'title', <query>, <page>]
    if (segments[1] === 'books' && segments[2] === 'find' && segments[3] === 'title' && segments[4]) {
        try {
            return decodeURIComponent(segments[4]);
        } catch {
            return segments[4];
        }
    }
    return new URLSearchParams(search).get('title') ?? '';
};

/**
 * useSearchScope reports which list a search is confined to.
 *
 * This used to be author-only and ephemeral: the confinement lived in React
 * context, so a reload silently widened the search to the whole library while
 * the list stayed filtered. The route is the state now — every scoped list
 * (author, series, genre, collection, favourites) confers a scope, and the
 * query rides the URL with it.
 *
 * Arriving in a scoped list turns the scope on, which is what a reader who
 * has just clicked through means. Leaving turns it off. In between the reader
 * may release it, and that decision stands until they cross into another
 * scope — a different list is a fresh decision.
 */
const useSearchScope = (onEnter?: OnEnter): SearchScope => {
    const { t } = useTranslation();
    const location = useLocation();
    const { authorId, authorName, setAuthorName } = useAuthor();

    const route = routeScopeFrom(location.pathname);
    const kind = route?.kind ?? null;
    const id = route?.id ?? '';
    const firstPagePath = route?.firstPagePath ?? '';

    const [released, setReleased] = useState(false);

    // The release belongs to one scope: crossing into another list — even the
    // same kind with another id — resets it. Starts empty so a cold open on a
    // scoped list still counts as a crossing and the scope comes on.
    const scopeKey = kind ? `${kind}:${id}` : '';
    const previousKey = useRef('');

    // onEnter is held in a ref so a caller passing a fresh closure on every
    // render does not re-fire the crossing. It is updated in an effect rather
    // than during render: a render that never commits would otherwise leave
    // the ref holding a callback belonging to a state that never existed.
    const enter = useRef(onEnter);
    useEffect(() => {
        enter.current = onEnter;
    });

    useEffect(() => {
        if (previousKey.current === scopeKey) {
            return;
        }
        previousKey.current = scopeKey;
        setReleased(false);
        if (kind) {
            enter.current?.();
        }
    }, [scopeKey, kind]);

    // The context caches one author name per id, so it is only this scope's
    // name when the cached id is the route's. A cold arrival has neither; the
    // list load puts the id in context, and only then is the name fetched —
    // fetching earlier would bank a name against the wrong id.
    const name = kind === 'author' && authorId === id ? authorName : '';
    useEffect(() => {
        if (kind !== 'author' || !id || authorId !== id || authorName) {
            return;
        }
        let cancelled = false;
        booksApi
            .getAuthor(id)
            .then((author) => {
                if (!cancelled && author?.full_name) setAuthorName(author.full_name);
            })
            .catch(() => {
                // Without a name the scope simply says less; it still works.
            });
        return () => {
            cancelled = true;
        };
    }, [kind, id, authorId, authorName, setAuthorName]);

    const label = (() => {
        switch (kind) {
            case 'author':
                return name ? t('searchWithinAuthor', { name }) : t('searchWithinThisAuthor');
            case 'series':
                return t('searchWithinThisSeries');
            case 'genre':
                return t('searchWithinThisGenre');
            case 'collection':
                return t('searchWithinThisCollection');
            case 'favorites':
                return t('searchWithinFavorites');
            default:
                return '';
        }
    })();

    return {
        kind,
        id,
        active: kind !== null && !released,
        firstPagePath,
        label,
        release: () => setReleased(true),
        reclaim: () => setReleased(false),
    };
};

export default useSearchScope;
