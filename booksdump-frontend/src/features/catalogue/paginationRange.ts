import { pagesInQuery, setPage } from '@/features/catalogue/addresses';

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
    /**
     * Width of a numbered cell by how many digits it holds: `cells[0]` for one
     * digit, up to the last page's length. A cell is as wide as numbers of its
     * own length draw, so `1` does not take the width of `48159`.
     */
    cells: number[];
    /** Width of one ellipsis. */
    ellipsis: number;
    /** Gap between two neighbouring items. */
    gap: number;
    /** Space to leave between the arrows and the numbers. */
    breathing: number;
}

/** What the measuring rig reports about the type a pager is drawn in. */
export interface CellProbe {
    /** Width of a cell with no digits in it: its padding and its border. */
    pad: number;
    /** Width of the widest number as long as the last page. */
    widest: number;
    /** How many digits the last page has. */
    digits: number;
    /** The least a cell may be, however few digits it holds. */
    floor: number;
}

/**
 * cellWidths turns one reading of the rig into a width per digit count.
 *
 * Every cell used to take the width of the widest number the last page could
 * draw, which kept one grid and stretched `1` to the width of `48159`. The grid
 * was not what mattered. The pager has to know what a window costs without
 * reading it off the window, and that needs a width that depends on the type
 * alone. A width per digit count is such a thing: it is worked out from probes,
 * so which pages are on screen has no say in it, and numbers of one length still
 * line up.
 *
 * The widest number holds one digit, repeated, so a shorter number of that digit
 * is the padding plus its share. That is the most any number of that length can
 * draw; rounded up, because a fractional minimum wraps it.
 */
export function cellWidths({ pad, widest, digits, floor }: CellProbe): number[] {
    const advance = (widest - pad) / digits;
    return Array.from({ length: digits }, (_, index) =>
        Math.max(floor, Math.ceil(pad + advance * (index + 1))),
    );
}

/** cellWidth reads a number's width off the table, by its length. */
export function cellWidth(page: number, cells: number[]): number {
    return cells[Math.min(String(page).length, cells.length) - 1];
}

/**
 * pagerRowWidth predicts what a window of items would measure.
 *
 * Numbers first, then the two arrows and the gaps that flank them. Each cell's
 * width comes from its length, not from a reading of it, so nothing here moves
 * when the window does — which is what makes fitting a window a single
 * calculation rather than a render-and-remeasure loop.
 */
export function pagerRowWidth(items: PageItem[], metrics: PagerMetrics): number {
    if (items.length === 0) {
        return 0;
    }
    const numbers = items.reduce<number>(
        (sum, item) =>
            sum + (item === 'ellipsis' ? metrics.ellipsis : cellWidth(item, metrics.cells)),
        0,
    );
    return numbers + (items.length - 1) * metrics.gap + 2 * (metrics.arrow + metrics.breathing);
}

/**
 * siblingCeiling says where fitSiblingCount's search has to start.
 *
 * The window used to be capped at three neighbours however wide the row was,
 * which is what left several hundred pixels of air at either end of a desktop
 * column. Nothing but the measurement should stop it — but the search still
 * needs somewhere to start, because counting down from a catalogue's 44 500
 * pages would rebuild a 44 500-entry range at every step.
 *
 * Two bounds, both cheap. Every item in the row is at least as wide as the
 * slimmest cell or an ellipsis, so the row cannot hold more than that many
 * items; and one extra sibling adds two items, so half the item count
 * bounds the siblings. Past half the catalogue the window already covers every
 * page and widening it changes nothing.
 */
export function siblingCeiling(totalPages: number, metrics: PagerMetrics): number {
    const slot = Math.min(...metrics.cells, metrics.ellipsis) + metrics.gap;
    if (slot <= 0) {
        return 0;
    }
    // The last item pays no gap, so it is added back before dividing.
    const forItems = metrics.row - 2 * (metrics.arrow + metrics.breathing) + metrics.gap;
    const items = Math.floor(forItems / slot);
    // A window of s siblings is at least 2s + 1 items wide. A row too narrow
    // to hold anything drives this negative, which the clamp below turns into
    // the only honest answer: no window at all.
    const byWidth = Math.floor((items - 1) / 2);
    return Math.max(0, Math.min(byWidth, Math.ceil(totalPages / 2)));
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
 * `maxSiblings` is where to start counting down from, not a policy: left out,
 * it comes from siblingCeiling, so the row's own measurement is what stops the
 * window rather than a constant.
 *
 * Returns the largest sibling count whose window fits, or 0 if even the
 * narrowest window overflows: a cramped pager still beats one whose arrows sit
 * off the screen, and `main` clips rather than scrolls.
 */
export function fitSiblingCount(
    currentPage: number,
    totalPages: number,
    metrics: PagerMetrics,
    { boundaryCount = 1, maxSiblings }: { boundaryCount?: number; maxSiblings?: number } = {},
): number {
    const ceiling = maxSiblings ?? siblingCeiling(totalPages, metrics);
    for (let siblings = ceiling; siblings > 0; siblings -= 1) {
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
 * clampPage reads a page number typed by hand and holds it to the list. Whatever
 * is not a digit goes first: a paste brings spaces, and a phone's numeric
 * keyboard still has a few other keys. Past the end means the end, not a page
 * that does not exist. With no digits at all there is no page to go to.
 */
export function clampPage(raw: string, totalPages: number): number | null {
    const digits = raw.replace(/\D/g, '');
    if (digits === '') {
        return null;
    }
    return Math.min(Math.max(Number(digits), 1), totalPages);
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
 * would silently widen the results on page two. A search and an author's books
 * keep the page in that query instead of the path.
 */
export function pageHref(currentUrl: string, page: number): string {
    const question = currentUrl.indexOf('?');
    const pathname = question === -1 ? currentUrl : currentUrl.slice(0, question);
    const search = question === -1 ? '' : currentUrl.slice(question);
    if (pagesInQuery(pathname)) {
        const params = new URLSearchParams(search);
        setPage(params, page);
        const query = params.toString();
        return query ? `${pathname}?${query}` : pathname;
    }
    return `${pageBaseUrl(pathname)}/${page}${search}`;
}
