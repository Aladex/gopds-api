import { useEffect } from 'react';
import { useLocation } from 'react-router';

import type { Book } from '@/api/books';
import { useSearchBar } from '@/context/SearchBarContext';
import { routeScopeFrom } from '@/features/catalogue/hooks/useSearchScope';

/**
 * useScopeName publishes what the list a reader is standing in is called.
 *
 * The route carries an id and nothing else, and there is no public endpoint
 * that turns a genre or series id into a name — the admin catalogue has one,
 * but a reader is not an admin. The books on the page already carry it: a book
 * listed under a genre names that genre among its own, and the same holds for
 * a series. So the name is read off the loaded page rather than fetched.
 *
 * The author scope resolves its own name through AuthorContext, which caches
 * it across pages and can answer before any book has loaded; this covers the
 * scopes that have no such context.
 *
 * It clears on leaving so a stale name cannot label the next list, and it
 * stays empty until books arrive — a chip that says "search in this genre"
 * for a moment is honest, one that says the wrong genre is not.
 */
const useScopeName = (books: Book[]) => {
    const location = useLocation();
    const { scopeName, setScopeName } = useSearchBar();

    const route = routeScopeFrom(location.pathname);
    const kind = route?.kind ?? null;
    const id = route?.id ?? '';

    const found = nameFromBooks(books, kind, id);

    useEffect(() => {
        if (!kind || kind === 'author') {
            if (scopeName) setScopeName('');
            return;
        }
        if (found && found !== scopeName) {
            setScopeName(found);
        }
    }, [kind, found, scopeName, setScopeName]);

    // Leaving one scoped list for another must not carry the old name over
    // while the new page loads.
    useEffect(() => {
        return () => setScopeName('');
    }, [kind, id, setScopeName]);
};

/** nameFromBooks finds the scoped entity among what the page already loaded. */
export const nameFromBooks = (books: Book[], kind: string | null, id: string): string => {
    if (!kind || !id || books.length === 0) {
        return '';
    }
    const wanted = Number(id);
    for (const book of books) {
        if (kind === 'genre') {
            const genre = book.genres?.find((g) => g.id === wanted);
            if (genre?.genre) return genre.genre;
        }
        if (kind === 'series') {
            const series = book.series?.find((s) => s.id === wanted);
            if (series?.ser) return series.ser;
        }
    }
    return '';
};

export default useScopeName;
