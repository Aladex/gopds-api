import { paginationRange, pageBaseUrl } from '@/features/catalogue/paginationRange';

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
