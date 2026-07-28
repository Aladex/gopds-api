/**
 * paginationRange decides which page numbers a pager shows.
 *
 * The catalogue runs to tens of thousands of pages, so the pager always shows
 * the first and last few, a window around the current page, and an ellipsis
 * wherever it skipped something. This reproduces the algorithm the MUI pager
 * used before, so the pager keeps behaving as readers are used to.
 */

export type PageItem = number | 'ellipsis';

export interface PaginationRangeOptions {
    /** How many pages to always show at each end. */
    boundaryCount?: number;
    /** How many pages to show either side of the current one. */
    siblingCount?: number;
}

const range = (start: number, end: number): number[] =>
    end < start ? [] : Array.from({ length: end - start + 1 }, (_, index) => start + index);

export function paginationRange(
    currentPage: number,
    totalPages: number,
    { boundaryCount = 3, siblingCount = 3 }: PaginationRangeOptions = {},
): PageItem[] {
    if (totalPages <= 0) {
        return [];
    }

    const page = Math.min(Math.max(currentPage, 1), totalPages);

    const startPages = range(1, Math.min(boundaryCount, totalPages));
    const endPages = range(Math.max(totalPages - boundaryCount + 1, boundaryCount + 1), totalPages);

    // The sibling window is pulled back from the ends so it never overlaps the
    // boundary pages or leaves a gap of exactly one page — a lone ellipsis
    // standing in for a single number would be wider than the number.
    const siblingsStart = Math.max(
        Math.min(page - siblingCount, totalPages - boundaryCount - siblingCount * 2 - 1),
        boundaryCount + 2,
    );
    const siblingsEnd = Math.min(
        Math.max(page + siblingCount, boundaryCount + siblingCount * 2 + 2),
        endPages.length > 0 ? endPages[0] - 2 : totalPages - 1,
    );

    return [
        ...startPages,

        ...(siblingsStart > boundaryCount + 2
            ? (['ellipsis'] as PageItem[])
            : boundaryCount + 1 < totalPages - boundaryCount
              ? [boundaryCount + 1]
              : []),

        ...range(siblingsStart, siblingsEnd),

        ...(siblingsEnd < totalPages - boundaryCount - 1
            ? (['ellipsis'] as PageItem[])
            : totalPages - boundaryCount > boundaryCount
              ? [totalPages - boundaryCount]
              : []),

        ...endPages,
    ];
}

/**
 * pageBaseUrl strips a trailing page number off a route, so the pager can build
 * sibling URLs from wherever the reader currently is.
 */
export function pageBaseUrl(pathname: string): string {
    const segments = pathname.split('/');
    if (/^\d+$/.test(segments[segments.length - 1])) {
        segments.pop();
    }
    return segments.join('/');
}
