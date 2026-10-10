/**
 * The order of an ordinary book list. The server sorts by author on request —
 * by the first name of each book's author line — and serves the newest first
 * otherwise; a search keeps its ranking, favourites and collections their own
 * order, so there the choice is neither offered nor sent.
 */

export const BOOK_SORT_AUTHOR = 'author';

/** The list paths whose order the reader may choose. */
const SORTABLE_LIST =
    /^\/books\/(page|find\/author\/[^/]+|find\/category\/[^/]+|find\/genre\/[^/]+)\/\d+$/;

export function canSortByAuthor(pathname: string, params: URLSearchParams): boolean {
    return SORTABLE_LIST.test(pathname) && !params.get('title') && !params.get('book_id');
}

/** The query string with the sort set, or removed when sort is null. */
export function withSort(search: string, sort: typeof BOOK_SORT_AUTHOR | null): string {
    const params = new URLSearchParams(search);
    if (sort) {
        params.set('sort', sort);
    } else {
        params.delete('sort');
    }
    const rest = params.toString();
    return rest ? `?${rest}` : '';
}
