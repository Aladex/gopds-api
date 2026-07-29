import { http } from '@/api/http';

/** Book catalogue, favourites, language and theme preferences. */

export interface Author {
    id: number;
    full_name: string;
    /**
     * How many books the author holds under the filters being browsed with.
     * Only the author search reports it — a book listing its own authors does
     * not, so it is absent far more often than it is present.
     */
    books_count?: number;
}

export interface Genre {
    id: number;
    genre: string;
    section?: string;
}

export interface Series {
    id: number;
    ser: string;
    ser_no: number;
}

/**
 * Book mirrors models.Book on the backend.
 *
 * Note `cover` is a boolean — whether the book has one — not a URL. Components
 * used to declare it as a string locally, which nothing read, so the mistake
 * stayed invisible.
 */
export interface Book {
    id: number;
    title: string;
    authors: Author[];
    series: Series[];
    genres: Genre[];
    annotation: string;
    filename: string;
    cover: boolean;
    registerdate: string;
    docdate: string;
    lang: string;
    fav: boolean;
    approved: boolean;
    path: string;
    format: string;
    favorite_count: number;
    md5?: string;
    duplicate_hidden?: boolean;
    position?: number;
}

export interface BooksPage {
    books: Book[];
    length: number;
}

/** BooksQuery mirrors models.BookFilters on the backend, field for field. */
export interface BooksQuery {
    limit?: number;
    offset?: number;
    title?: string;
    author?: number | string;
    series?: number | string;
    genre?: number | string;
    lang?: string;
    fav?: boolean;
    unapproved?: boolean;
    users_favorites?: boolean;
    collection?: number | string;
    curated_collection?: number | string;
    include_hidden?: boolean;
}

export interface Language {
    lang: string;
    language_count: number;
}

export type ThemeMode = 'light' | 'dark';

export const listBooks = (query: BooksQuery) =>
    http.get<BooksPage>('/books/list', {
        query: query as Record<string, string | number | boolean | undefined>,
    });

export const listAuthors = (query: {
    author?: string;
    limit?: number;
    offset?: number;
    /**
     * The reader's books language, which has to be the one the book list uses:
     * the count each author is offered with is a count under this filter, and
     * the two disagreeing would make it a promise the next page breaks.
     */
    lang?: string;
}) => http.get<{ authors: Author[]; length: number }>('/books/authors', { query });

/**
 * getAuthor names a single author, which the search panel needs when a reader
 * arrives on an author's list by URL rather than by following a link — there is
 * no name in `/books/find/author/42/1` to put on screen.
 */
export const getAuthor = (id: number | string) =>
    http.post<Author>('/books/author', { author_id: Number(id) });

export const listLanguages = () => http.get<{ langs: Language[] }>('/books/langs');

/** toggleFavourite adds or removes a book from the caller's favourites. */
export const toggleFavourite = (bookID: number, fav: boolean) =>
    http.post<{ have_favs: boolean }>('/books/fav', { book_id: bookID, fav });

export const getThemePreference = () => http.get<{ theme?: ThemeMode }>('/books/theme');

export const setThemePreference = (theme: ThemeMode) => http.post<void>('/books/theme', { theme });
