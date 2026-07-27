import { useCallback, useEffect, useReducer, useRef } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';

import * as booksApi from '@/api/books';
import type { Book, BooksQuery } from '@/api/books';

import { useAuth } from '../../../context/AuthContext';
import { useAuthor } from '../../../context/AuthorContext';

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
    | { type: 'TOGGLE_FAV'; payload: number };

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
                books: action.payload.books,
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
                    b.id === action.payload ? { ...b, fav: !b.fav } : b,
                ),
            };
        default:
            return state;
    }
}

export function useBooksQuery() {
    const { user } = useAuth();
    const { page, id, title } = useParams<{ page: string; id?: string; title?: string }>();
    const { authorId, authorBook, setAuthorId, clearAuthorBook } = useAuthor();
    const location = useLocation();
    const navigate = useNavigate();
    const prevLangRef = useRef(user?.books_lang);
    const [state, dispatch] = useReducer(booksListReducer, initialState);

    const getParams = useCallback(() => {
        const limit = 10;
        const currentPage = parseInt(page || '1', 10);
        const offset = (currentPage - 1) * limit;
        const params: BooksQuery = { limit, offset, lang: user?.books_lang || '' };

        if (location.pathname.includes('/books/find/author/') && id) {
            params.author = id;
            setAuthorId(id);
            if (authorBook) params.title = authorBook;
        } else if (location.pathname.includes('/books/find/category/') && id) {
            params.series = id;
            clearAuthorBook();
        } else if (location.pathname.includes('/books/find/genre/') && id) {
            params.genre = id;
            clearAuthorBook();
        } else if (location.pathname.includes('/books/find/title/') && title) {
            params.title = decodeURIComponent(title);
            if (authorId) params.author = authorId;
            clearAuthorBook();
        } else if (location.pathname.startsWith('/collections/') && id) {
            params.curated_collection = id;
            clearAuthorBook();
        }

        if (location.pathname.includes('/books/favorite')) {
            params.fav = true;
            clearAuthorBook();
        }

        if (location.pathname.includes('/books/users/favorites')) {
            params.users_favorites = true;
            clearAuthorBook();
        }

        return params;
    }, [
        authorBook,
        authorId,
        clearAuthorBook,
        id,
        location.pathname,
        page,
        setAuthorId,
        title,
        user?.books_lang,
    ]);

    const loadBooks = useCallback(async () => {
        // Changing the reading language invalidates deep pages, so start over
        // rather than showing page 40 of a shorter list.
        if (prevLangRef.current !== user?.books_lang && page !== '1') {
            navigate('/books/page/1');
            return;
        }

        dispatch({ type: 'SET_LOADING' });

        try {
            window.scrollTo(0, 0);
            const data = await booksApi.listBooks(getParams());
            dispatch({ type: 'FETCH_SUCCESS', payload: { books: data.books, totalPages: data.length } });
        } catch (error) {
            console.error('Error fetching books', error);
            dispatch({ type: 'FETCH_ERROR' });
        }

        // Update language reference after successful fetch to prevent redirect loops
        prevLangRef.current = user?.books_lang;
    }, [getParams, navigate, page, user?.books_lang]);

    useEffect(() => {
        loadBooks();
    }, [loadBooks]);

    return { state, dispatch, loadBooks };
}
