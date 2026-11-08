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

/**
 * AuthorHit is an author as the author search and the author heading name
 * one: a catalogue author, or under the entity identity an author entity —
 * its UUID, and a label when another entity has the same name.
 */
export interface AuthorHit {
    id: number | string;
    full_name: string;
    /** A namesake's label: its dominant genre, or else (label_books) its book count. */
    label?: string;
    label_books?: number;
    /** How many books the author holds under the filters being browsed with. */
    books_count?: number;
}

/** One book of an author's pile. */
export interface AuthorCover {
    path: string;
    filename: string;
    title: string;
}

/**
 * AuthorCardData is an author as the search page shows one. The summary fields
 * are absent from a server without the summary and empty until it reaches the
 * author, so nothing here can be counted on.
 */
export interface AuthorCardData extends AuthorHit {
    /** 'person' or 'collective'. */
    kind?: string;
    genres?: string[] | null;
    works?: string[] | null;
    covers?: AuthorCover[] | null;
    /** The name the search matched, when it is closer than the one shown. */
    matched_name?: string;
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
    /**
     * From the book's current metadata snapshot: null and [] when it has none.
     * Only the book list sends them, so they are optional here.
     */
    publisher?: string | null;
    isbn?: string[];
    /** At most two credited translators; absent when the book has none. */
    translators?: string[];
    /** Set when the book credits more translators than `translators` names. */
    translators_more?: boolean;
    /**
     * The book's author line, sent only while the server shows authors from
     * the author layer: the names in the file's order, each with the
     * catalogue author it links to when there is one. Absent, the card shows
     * `authors`.
     */
    authors_display?: AuthorDisplay[];
    /**
     * Set on a grouped list (cards) for a card of more than one visible file:
     * the card's key, how many variants or revisions it holds, and why the
     * list shows it on this file rather than on its default. Absent on a
     * single book and on every per-file list.
     */
    variants?: BookVariantsInfo;
}

/** What a listed card says of its files. */
export interface BookVariantsInfo {
    key: string;
    /** Documents for a card of variants, files for a card of revisions. */
    count: number;
    kind: 'variants' | 'revisions';
    reason?: 'saved' | 'new_revision' | 'new_variant';
}

/** One visible file of a card, as the variants of a book list it. */
export interface BookVariantRow extends Book {
    /** The FB2 document the file is a revision of, or "book:<id>". */
    lineage: string;
    /** The newest revision of its document in the card. */
    latest: boolean;
    rank: number;
    /** The title's stripped trailing tags, joined with ", ". */
    tag: string;
    doc_version: string;
    /** The file the card opens on when nothing else is asked. */
    default: boolean;
}

/** A card with every visible file of it, by rank. */
export interface BookVariants {
    key: string;
    kind: string;
    count: number;
    default_id: number;
    variants: BookVariantRow[];
}

/** One name of a book's author line. */
export interface AuthorDisplay {
    name: string;
    /** The catalogue author the name links to; absent when it is none of them. */
    legacy_author_id?: number;
    /**
     * Under the entity identity, the author entities the name links to
     * instead: one, or every person of a name of several. Absent when it
     * links to none.
     */
    entities?: AuthorEntityRef[];
}

/** An author entity a name links to, as its page names it. */
export interface AuthorEntityRef {
    id: string;
    full_name: string;
    label?: string;
    label_books?: number;
}

export interface BooksPage {
    books: Book[];
    length: number;
}

/**
 * What the server actually sends for a page: a nil Go slice marshals to JSON
 * null, so a zero-hit answer carries "books": null. Kept separate from
 * BooksPage so the wire stays honest while consumers are promised an array.
 */
interface BooksPageWire {
    books: Book[] | null;
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
    /** Pins the result to one exact book — the pick an autocomplete offered. */
    book_id?: number | string;
    /** Asks a scoped list for one entry per card instead of one per file. */
    cards?: boolean;
}

export interface Language {
    lang: string;
    language_count: number;
}

export type ThemeMode = 'light' | 'dark';

export const listBooks = async (query: BooksQuery): Promise<BooksPage> => {
    const data = await http.get<BooksPageWire>('/books/list', {
        query: query as Record<string, string | number | boolean | undefined>,
    });
    // Normalise once at the boundary so every consumer gets an array even
    // when the server answers a zero-hit search with "books": null.
    return { ...data, books: data.books ?? [] };
};

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
}) =>
    http.get<{
        authors: AuthorCardData[];
        /** Pages. */
        length: number;
        /** Authors found; not sent by a server that has no author summary yet. */
        total?: number;
    }>('/books/authors', { query });

/**
 * getAuthor names a single author, which the search chip needs when a reader
 * arrives on an author's list by URL rather than by following a link — there is
 * no name in `/author/42` to put on screen. Under the entity
 * identity the id is the entity's UUID and the answer carries its label.
 */
export const getAuthor = (id: number | string) =>
    http.post<AuthorHit>('/books/author', {
        // A legacy author goes as the number it is, an entity as its UUID.
        author_id: /^\d+$/.test(String(id)) ? Number(id) : String(id),
    });

export const listLanguages = () => http.get<{ langs: Language[] }>('/books/langs');

/**
 * toggleFavourite adds or removes a book from the caller's favourites. With
 * wholeGroup, removing takes every file of the book's card off them.
 */
export const toggleFavourite = (bookID: number, fav: boolean, wholeGroup?: boolean) =>
    http.post<{ have_favs: boolean }>(
        '/books/fav',
        wholeGroup ? { book_id: bookID, fav, whole_group: true } : { book_id: bookID, fav },
    );

/** getBookVariants reads every visible file of the book's card, by rank. */
export const getBookVariants = (bookID: number) =>
    http.get<BookVariants>(`/books/variants/${bookID}`);

export const getThemePreference = () => http.get<{ theme?: ThemeMode }>('/books/theme');

export const setThemePreference = (theme: ThemeMode) => http.post<void>('/books/theme', { theme });
