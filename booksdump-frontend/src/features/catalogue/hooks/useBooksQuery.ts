import { useCallback, useEffect, useReducer, useRef } from 'react';
import { useLocation, useNavigate, useParams, useSearchParams } from 'react-router';

import * as booksApi from '@/api/books';
import type { Book, BooksQuery } from '@/api/books';

import { useAuth } from '@/context/AuthContext';
import { useAuthor } from '@/context/AuthorContext';
import {
    isAuthorPath,
    isSearchPath,
    pageFromQuery,
    pagesInQuery,
} from '@/features/catalogue/addresses';
import { pageHref } from '@/features/catalogue/paginationRange';
import { scrollToLoadedList, usePageTurn } from '@/shared/layout/landOnList';

/**
 * useBooksQuery owns everything about *which* books are on screen: it derives
 * the query from the route, fetches, and holds the resulting list.
 *
 * Extracted from BooksList unchanged, so that presentation can be rebuilt
 * without the loading rules moving at the same time.
 */

export interface BooksListState {
    books: Book[];
    loading: boolean;
    totalPages: number;
    menuLoading: boolean;
    selectedBook: number | null;
}

export type BooksListAction =
    | { type: 'FETCH_SUCCESS'; payload: { books: Book[]; totalPages: number } }
    | { type: 'FETCH_ERROR' }
    | { type: 'SET_LOADING' }
    | { type: 'SET_MENU_LOADING'; payload: boolean }
    | { type: 'SET_SELECTED_BOOK'; payload: number | null }
    | { type: 'UPDATE_BOOK'; payload: Book }
    /** Flips the star of a card: a book's id, or the key of a card of variants. */
    | { type: 'TOGGLE_FAV'; payload: number | string };

const initialState: BooksListState = {
    books: [],
    loading: true,
    totalPages: 0,
    menuLoading: false,
    selectedBook: null,
};

export function booksListReducer(state: BooksListState, action: BooksListAction): BooksListState {
    switch (action.type) {
        case 'FETCH_SUCCESS':
            return {
                ...state,
                // The wire does not honour the Book[] type: a nil Go slice
                // marshals to JSON null, and a zero-hit search arrives as
                // { books: null, length: 0 }. Normalise here so every
                // consumer of state.books can trust it is an array.
                books: action.payload.books ?? [],
                totalPages: action.payload.totalPages,
                loading: false,
            };
        case 'FETCH_ERROR':
            return { ...state, loading: false };
        case 'SET_LOADING':
            return { ...state, loading: true };
        case 'SET_MENU_LOADING':
            return { ...state, menuLoading: action.payload };
        case 'SET_SELECTED_BOOK':
            return { ...state, selectedBook: action.payload };
        case 'UPDATE_BOOK':
            return {
                ...state,
                books: state.books.map((b) => (b.id === action.payload.id ? action.payload : b)),
            };
        case 'TOGGLE_FAV':
            return {
                ...state,
                books: state.books.map((b) =>
                    (b.variants?.key ?? b.id) === action.payload ? { ...b, fav: !b.fav } : b,
                ),
            };
        default:
            return state;
    }
}

export function useBooksQuery() {
    const { user } = useAuth();
    const { page: pageInPath, id } = useParams<{ page: string; id?: string }>();
    const { setAuthorId } = useAuthor();
    const location = useLocation();
    const navigate = useNavigate();
    const [searchParams] = useSearchParams();
    const prevLangRef = useRef(user?.books_lang);
    const [state, dispatch] = useReducer(booksListReducer, initialState);
    // The page is a string to match the path it comes from on the other lists,
    // and always set here: the first page of these addresses has no parameter.
    const page = pagesInQuery(location.pathname) ? String(pageFromQuery(searchParams)) : pageInPath;

    const getParams = useCallback(() => {
        const limit = 10;
        const currentPage = parseInt(page || '1', 10);
        const offset = (currentPage - 1) * limit;
        const params: BooksQuery = { limit, offset, lang: user?.books_lang || '' };

        if (isAuthorPath(location.pathname) && id) {
            params.author = id;
            setAuthorId(id);
        } else if (location.pathname.includes('/books/find/category/') && id) {
            params.series = id;
        } else if (location.pathname.includes('/books/find/genre/') && id) {
            params.genre = id;
        } else if (isSearchPath(location.pathname)) {
            const query = searchParams.get('q');
            if (query) {
                params.title = query;
            }
        } else if (location.pathname.startsWith('/collections/') && id) {
            params.curated_collection = id;
        }

        if (location.pathname.includes('/books/favorite')) {
            params.fav = true;
        }

        if (location.pathname.includes('/books/users/favorites')) {
            params.users_favorites = true;
        }

        // A scoped search writes its query next to the route's own filters
        // rather than into React state, so a reload asks for exactly what the
        // reader last saw. A book search already carries its words in q, and
        // they win there.
        const scopedTitle = searchParams.get('title');
        if (scopedTitle && !params.title) {
            params.title = scopedTitle;
        }
        const bookId = searchParams.get('book_id');
        if (bookId) {
            params.book_id = bookId;
        }

        // One card per book on the feed and the scoped lists; a search and a
        // pinned book stay one row per file.
        if (!params.title && !params.book_id) {
            params.cards = true;
        }

        return params;
    }, [id, location.pathname, page, searchParams, setAuthorId, user?.books_lang]);

    const loadBooks = useCallback(
        async (turned = false) => {
            // Changing the reading language invalidates deep pages, so start over
            // rather than showing page 40 of a shorter list. In place: the list
            // and its search survive, only the page number goes.
            const sameLanguage = prevLangRef.current === user?.books_lang;
            if (!sameLanguage && page !== '1') {
                navigate(pageHref(location.pathname + location.search, 1));
                return;
            }

            dispatch({ type: 'SET_LOADING' });

            try {
                scrollToLoadedList(turned && sameLanguage);
                const data = await booksApi.listBooks(getParams());
                dispatch({
                    type: 'FETCH_SUCCESS',
                    payload: { books: data.books, totalPages: data.length },
                });
            } catch (error) {
                console.error('Error fetching books', error);
                dispatch({ type: 'FETCH_ERROR' });
            }

            // Update language reference after successful fetch to prevent redirect loops
            prevLangRef.current = user?.books_lang;
        },
        [getParams, location, navigate, page, user?.books_lang],
    );

    const turned = usePageTurn();
    useEffect(() => {
        loadBooks(turned);
    }, [loadBooks, turned]);

    return { state, dispatch, loadBooks, page: parseInt(page as string, 10) };
}
