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

/** What one rendered pager row costs, in CSS pixels, as measured off the DOM. */
export interface PagerMetrics {
    /** Width of the row's own box — the column the pager has to live inside. */
    row: number;
    /** Width of one step arrow. */
    arrow: number;
    /** Width of one numbered cell; every cell of a pager shares a min-width. */
    cell: number;
    /** Width of one ellipsis. */
    ellipsis: number;
    /** Gap between two neighbouring items. */
    gap: number;
    /** Space to leave between the arrows and the numbers. */
    breathing: number;
}

/**
 * pagerRowWidth predicts what a window of items would measure.
 *
 * Numbers first, then the two arrows and the gaps that flank them. The cell
 * width is the shared min-width tier, so it does not move when the window
 * grows — which is what makes fitting a window a single calculation rather
 * than a render-and-remeasure loop.
 */
export function pagerRowWidth(items: PageItem[], metrics: PagerMetrics): number {
    if (items.length === 0) {
        return 0;
    }
    const cells = items.filter((item) => item !== 'ellipsis').length;
    const dots = items.length - cells;
    const numbers =
        cells * metrics.cell + dots * metrics.ellipsis + (items.length - 1) * metrics.gap;
    return numbers + 2 * (metrics.arrow + metrics.breathing);
}

/**
 * fitSiblingCount picks the widest window of page numbers that still fits.
 *
 * The pager used to guess this from the digit count of the last page, and the
 * guess was wrong three times running: arithmetic said a five-digit row came
 * to 300px where the browser measured 329. Measuring the row and asking what
 * fits removes the guess — and it adapts to the things a digit count cannot
 * see, like a 320px phone or a reader who scaled the type up.
 *
 * Returns the largest sibling count whose window fits, or 0 if even the
 * narrowest window overflows: a cramped pager still beats one whose arrows sit
 * off the screen, and `main` clips rather than scrolls.
 */
export function fitSiblingCount(
    currentPage: number,
    totalPages: number,
    metrics: PagerMetrics,
    { boundaryCount = 1, maxSiblings = 3 }: { boundaryCount?: number; maxSiblings?: number } = {},
): number {
    for (let siblings = maxSiblings; siblings > 0; siblings -= 1) {
        const items = paginationRange(currentPage, totalPages, {
            boundaryCount,
            siblingCount: siblings,
        });
        if (pagerRowWidth(items, metrics) <= metrics.row) {
            return siblings;
        }
    }
    return 0;
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

/**
 * pageHref builds the address of a sibling page from where the reader is:
 * same list, same search, different page. The query string rides along
 * untouched — it carries the search that filtered the list, and dropping it
 * would silently widen the results on page two.
 */
export function pageHref(currentUrl: string, page: number): string {
    const question = currentUrl.indexOf('?');
    const pathname = question === -1 ? currentUrl : currentUrl.slice(0, question);
    const search = question === -1 ? '' : currentUrl.slice(question);
    return `${pageBaseUrl(pathname)}/${page}${search}`;
}
