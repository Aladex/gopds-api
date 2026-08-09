import {
    fitSiblingCount,
    pageBaseUrl,
    pageHref,
    pagerRowWidth,
    paginationRange,
    type PageItem,
} from '@/features/catalogue/paginationRange';

// The pager is the only way to reach page 40000 of the catalogue, so its
// arithmetic is worth pinning down: an off-by-one here either hides the last
// page or renders an ellipsis standing in for a single number.

describe('paginationRange', () => {
    it('lists every page when they all fit', () => {
        expect(paginationRange(1, 5)).toEqual([1, 2, 3, 4, 5]);
    });

    it('keeps both ends reachable in a huge catalogue', () => {
        const items = paginationRange(1, 47377);

        expect(items.slice(0, 3)).toEqual([1, 2, 3]);
        expect(items.slice(-3)).toEqual([47375, 47376, 47377]);
        expect(items).toContain('ellipsis');
    });

    it('centres the window on the current page', () => {
        const items = paginationRange(500, 47377);

        expect(items).toEqual([
            1,
            2,
            3,
            'ellipsis',
            497,
            498,
            499,
            500,
            501,
            502,
            503,
            'ellipsis',
            47375,
            47376,
            47377,
        ]);
    });

    it('never elides a single page — it prints the number instead', () => {
        // A gap of exactly one would otherwise become an ellipsis wider than the
        // number it replaces.
        const items = paginationRange(1, 12);

        expect(items).not.toContain('ellipsis');
        expect(items).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]);
    });

    it('shows fewer pages on a narrow screen', () => {
        const items = paginationRange(500, 47377, { boundaryCount: 1, siblingCount: 1 });

        expect(items).toEqual([1, 'ellipsis', 499, 500, 501, 'ellipsis', 47377]);
    });

    it('holds the window inside the range at either edge', () => {
        const atEnd = paginationRange(47377, 47377);
        expect(atEnd[atEnd.length - 1]).toBe(47377);
        expect(Math.min(...atEnd.filter((i): i is number => typeof i === 'number'))).toBe(1);

        const atStart = paginationRange(1, 47377);
        expect(atStart[0]).toBe(1);
    });

    it('survives a page number outside the range', () => {
        // A hand-edited URL should not produce a broken pager.
        expect(paginationRange(0, 10)).toEqual(paginationRange(1, 10));
        expect(paginationRange(99, 10)).toEqual(paginationRange(10, 10));
    });

    it('has nothing to show for an empty result set', () => {
        expect(paginationRange(1, 0)).toEqual([]);
    });
});

// Every number below was measured in Chrome on the stand at the stated
// viewport, by reading the rendered pager off the DOM — not derived. Three
// earlier rounds of arithmetic disagreed with the browser by up to 29px, which
// is how the arrows ended up off the screen, so these are the record of what
// the layout actually costs.
const phone360 = {
    // 360px viewport, 328px content column, cells at the five-digit tier.
    row: 328,
    arrow: 28,
    cell: 48,
    ellipsis: 28,
    gap: 2,
    breathing: 8,
};
const phone360FourDigits = { ...phone360, cell: 40 };
// The ellipsis is an aria-hidden span holding a 16px glyph, and nobody taps
// it, so on a phone it drops from a button-sized 28px square to 20px.
const phone360SlimDots = { ...phone360FourDigits, ellipsis: 20 };
const phone320SlimDots = { ...phone360SlimDots, row: 288 };

describe('pagerRowWidth', () => {
    it('matches what the browser measured', () => {
        // Measured: the numbers of this window came to 208px, and the row it
        // sat in to 328px including both arrows.
        const items: PageItem[] = [1, 'ellipsis', 22000, 'ellipsis', 47377];

        expect(pagerRowWidth(items, { ...phone360, breathing: 2 })).toBe(268);
    });

    it('costs nothing for an empty window', () => {
        expect(pagerRowWidth([], phone360)).toBe(0);
    });
});

describe('fitSiblingCount', () => {
    it('gives up the neighbours when five-digit numbers leave no room', () => {
        // Measured on the stand: a five-digit window with neighbours needs
        // 308px of numbers where only 272px sits between the arrows. Shrinking
        // the ellipses to the bare glyph still leaves it 12px short.
        expect(fitSiblingCount(22000, 47377, phone360, { maxSiblings: 3 })).toBe(0);
    });

    it('spends a button-sized ellipsis on the neighbours it could have shown', () => {
        // Four digits with neighbours and wide ellipses measured 268px of
        // numbers against 272px between the arrows — it technically fits, but
        // only by pressing the digits against the arrows.
        expect(fitSiblingCount(555, 1037, phone360FourDigits, { maxSiblings: 3 })).toBe(0);
    });

    it('keeps the neighbours once the ellipsis is only as wide as its glyph', () => {
        // The same window with 20px ellipses measured 252px, which leaves the
        // numbers 10px clear of each arrow.
        expect(fitSiblingCount(555, 1037, phone360SlimDots, { maxSiblings: 3 })).toBe(1);
    });

    it('gives them up again on a 320px phone', () => {
        // The column drops to 288px there, leaving 232px between the arrows —
        // less than the 252px the neighbours need however thin the ellipsis.
        expect(fitSiblingCount(555, 1037, phone320SlimDots, { maxSiblings: 3 })).toBe(0);
    });

    it('opens the window right up on a desktop row', () => {
        expect(fitSiblingCount(22000, 47377, { ...phone360, row: 1200 }, { maxSiblings: 3 })).toBe(
            3,
        );
    });

    it('returns nothing rather than overflowing when even one page is too wide', () => {
        // `main` clips rather than scrolls, so an overflowing row hides the
        // arrows entirely — the failure this whole calculation exists to avoid.
        expect(fitSiblingCount(5, 100, { ...phone360, row: 40 })).toBe(0);
    });
});

describe('pageBaseUrl', () => {
    it('drops the page number so siblings can be built from it', () => {
        expect(pageBaseUrl('/books/page/1')).toBe('/books/page');
        expect(pageBaseUrl('/books/find/author/42/7')).toBe('/books/find/author/42');
    });

    it('leaves a route that does not end in a page number alone', () => {
        expect(pageBaseUrl('/books/favorite')).toBe('/books/favorite');
    });

    it('keeps an id that is not the page number', () => {
        // The author id is numeric too; only the final segment is a page.
        expect(pageBaseUrl('/books/find/author/42')).toBe('/books/find/author');
    });
});

describe('pageHref', () => {
    it('replaces only the page number', () => {
        expect(pageHref('/books/page/5', 7)).toBe('/books/page/7');
        expect(pageHref('/books/find/author/42/3', 4)).toBe('/books/find/author/42/4');
    });

    it('carries the search over to the sibling page', () => {
        // The query string holds the search that filtered the list; dropping
        // it on page 2 would silently widen the results.
        expect(pageHref('/books/find/author/42/3?title=%D1%85&book_id=5', 2)).toBe(
            '/books/find/author/42/2?title=%D1%85&book_id=5',
        );
    });

    it('keeps the search on a scoped favourites list', () => {
        expect(pageHref('/books/favorite/2?title=%D1%85', 1)).toBe(
            '/books/favorite/1?title=%D1%85',
        );
    });
});
