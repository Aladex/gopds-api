/**
 * The addresses of a search and of an author's books.
 *
 * Both put the page in the query and leave it out on the first page, so the
 * address a reader shares is the short one. A query travels as a parameter,
 * which is what lets a slash or a percent sign survive a reload: as a path
 * segment it passed through the router's own decoding first.
 */

export type SearchType = 'books' | 'authors';

export const SEARCH_PATH = '/search';

const SEARCH_ROUTE = /^\/search\/?$/;
const AUTHOR_ROUTE = /^\/author\/[^/]+\/?$/;

export const isSearchPath = (pathname: string): boolean => SEARCH_ROUTE.test(pathname);

export const isAuthorPath = (pathname: string): boolean => AUTHOR_ROUTE.test(pathname);

/**
 * Whether the books language narrows what this address shows. It filters every
 * list of books and the search for authors. The index of collections takes no
 * language, and neither does anything that is not a list.
 */
export const booksLanguageApplies = (pathname: string): boolean =>
    /^\/(books\/|author\/|search\/?$|collections\/\d+\/page(\/|$))/.test(pathname);

/** Whether a list at this address keeps its page in the query, not the path. */
export const pagesInQuery = (pathname: string): boolean =>
    isSearchPath(pathname) || isAuthorPath(pathname);

/** The address of a search, on its first page. */
export const searchPath = (query: string, type: SearchType = 'books'): string =>
    `${SEARCH_PATH}?${new URLSearchParams({ q: query, type })}`;

/** The address of an author's books: a legacy number or an entity's UUID. */
export const authorPath = (id: number | string): string =>
    `/author/${encodeURIComponent(String(id))}`;

/** A whole number above zero, or null for anything else an address can hold. */
const positivePage = (raw: string | null): number | null => {
    if (raw === null || !/^\d+$/.test(raw)) {
        return null;
    }
    const page = Number(raw);
    return page >= 1 ? page : null;
};

/** The page a query names; anything that is not a page number is the first. */
export const pageFromQuery = (params: URLSearchParams): number =>
    positivePage(params.get('page')) ?? 1;

/** Writes the page into a query, where the first page is the lack of one. */
export const setPage = (params: URLSearchParams, page: number): void => {
    if (page > 1) {
        params.set('page', String(page));
    } else {
        params.delete('page');
    }
};

const withQuery = (pathname: string, params: URLSearchParams) => {
    const query = params.toString();
    return { pathname, search: query ? `?${query}` : '' };
};

/**
 * legacySearchAddress moves /books/find/title/<query>/<page> and
 * /authors/<query>/<page>. The path is what the old address meant, so it wins
 * over any parameter of the same name; every other parameter rides along.
 */
export const legacySearchAddress = (
    query: string,
    type: SearchType,
    page: string | undefined,
    search: string,
) => {
    const params = new URLSearchParams(search);
    params.set('q', query);
    params.set('type', type);
    setPage(params, positivePage(page ?? null) ?? 1);
    return withQuery(SEARCH_PATH, params);
};

/** legacyAuthorAddress moves /books/find/author/<id>/<page>. */
export const legacyAuthorAddress = (id: string, page: string | undefined, search: string) => {
    const params = new URLSearchParams(search);
    setPage(params, positivePage(page ?? null) ?? 1);
    return withQuery(authorPath(id), params);
};
