import { useEffect, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';

import * as booksApi from '@/api/books';
import { useAuthor } from '@/context/AuthorContext';

const AUTHOR_BOOKS_PATH = '/books/find/author/';

export interface AuthorScope {
    /** Whether the reader is looking at one author's books at all. */
    available: boolean;
    /** Whether the next search will be confined to that author. */
    active: boolean;
    /** The author's name, once known; empty while it is being looked up. */
    name: string;
    /** The author's id, for the request that does the confining. */
    id: string;
    /** Turns the scope off — the reader asking to search the whole library. */
    release: () => void;
}

/**
 * useAuthorScope reports whether a search is confined to one author's books.
 *
 * This used to be a third entry in the search-mode dropdown, sitting next to
 * "by author" and reading almost identically to it while doing something
 * entirely different — and it named no author, so even a reader who opened the
 * dropdown could not tell whose books they were about to search. Hardly anyone
 * found it. The scope is now shown next to the query box instead, named, and
 * removable; this hook is the state behind it.
 *
 * Arriving at an author's list turns the scope on, which is what a reader who
 * has just clicked an author's name means. Leaving turns it off. In between the
 * reader may release it, and that decision stands until they cross a boundary
 * again.
 */
const useAuthorScope = (): AuthorScope => {
    const location = useLocation();
    const { authorId, authorName, setAuthorName } = useAuthor();

    const available = location.pathname.startsWith(AUTHOR_BOOKS_PATH);
    const [released, setReleased] = useState(false);

    // Starts at false so arriving straight on an author's list still counts as
    // a crossing, and the scope comes on for a reader who opened the page cold.
    const wasAvailable = useRef(false);

    useEffect(() => {
        if (wasAvailable.current === available) {
            return;
        }
        wasAvailable.current = available;
        setReleased(false);
    }, [available]);

    // The name is only fetched when nobody put it in the context on the way
    // here — following a link from a book card does, a pasted URL does not.
    useEffect(() => {
        if (!available || !authorId || authorName) {
            return;
        }
        let cancelled = false;
        booksApi
            .getAuthor(authorId)
            .then((author) => {
                if (!cancelled && author?.full_name) setAuthorName(author.full_name);
            })
            .catch(() => {
                // Without a name the scope simply says less; it still works.
            });
        return () => {
            cancelled = true;
        };
    }, [available, authorId, authorName, setAuthorName]);

    return {
        available,
        active: available && !released,
        name: authorName,
        id: authorId,
        release: () => setReleased(true),
    };
};

export default useAuthorScope;
