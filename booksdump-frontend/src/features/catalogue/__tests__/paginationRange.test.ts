import {
    cellWidths,
    clampPage,
    fitSiblingCount,
    pageBaseUrl,
    pageHref,
    pagerRowWidth,
    paginationRange,
    siblingCeiling,
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
/** Cells of one width whatever their digits: the arithmetic these rows pin down. */
const even = (cell: number) => Array.from({ length: 6 }, () => cell);

const phone360 = {
    // 360px viewport, 328px content column, cells at the five-digit tier.
    row: 328,
    arrow: 28,
    cells: even(48),
    ellipsis: 28,
    gap: 2,
    breathing: 8,
};
const phone360FourDigits = { ...phone360, cells: even(40) };
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

// Measured in Chrome on the stand with the pager bound to the card column:
// 1200px of column at 1440px of viewport, 977px at 1024px. The numbered cell
// is what five digits actually draw in tabular figures — 38px of text inside
// 8px of padding either side — not the 56px tier the ladder used to hand out.
const desktop1440 = {
    row: 1200,
    arrow: 36,
    cells: even(54),
    ellipsis: 36,
    gap: 4,
    breathing: 8,
};
const desktop1024 = { ...desktop1440, row: 977 };

// Page numbers drawn at 7.23px a digit inside 17.33px of padding and border,
// the figures Chrome gives the numbered cells on a desktop, with the 36px floor.
const byDigits = {
    ...desktop1440,
    cells: cellWidths({ pad: 17.33, widest: 53.5, digits: 5, floor: 36 }),
};

describe('cellWidths', () => {
    it('gives each digit count what its digits draw, up to the last page', () => {
        expect(cellWidths({ pad: 16, widest: 56, digits: 5, floor: 0 })).toEqual([
            24, 32, 40, 48, 56,
        ]);
    });

    it('holds a short number at the floor instead of at the last page', () => {
        // The complaint: `1` stretched to the width of `48159` to keep a grid.
        const [one, two, three, , five] = cellWidths({
            pad: 16,
            widest: 56,
            digits: 5,
            floor: 36,
        });

        expect(one).toBe(36);
        expect(two).toBe(36);
        expect(three).toBe(40);
        expect(five).toBe(56);
    });

    it('rounds up, because a fractional minimum wraps the widest number', () => {
        expect(cellWidths({ pad: 17.33, widest: 53.5, digits: 5, floor: 0 }).at(-1)).toBe(54);
    });

    it('answers for a one-digit catalogue without dividing by nothing else', () => {
        expect(cellWidths({ pad: 17, widest: 25, digits: 1, floor: 36 })).toEqual([36]);
        expect(cellWidths({ pad: 17, widest: 41, digits: 1, floor: 36 })).toEqual([41]);
    });
});

describe('pagerRowWidth with cells by their own digits', () => {
    it('charges every number its own width', () => {
        const metrics = { ...desktop1440, cells: [36, 36, 40, 48, 56] };

        // 36 + 36 + 56, one ellipsis, three gaps, and both arrows with their air.
        expect(pagerRowWidth([1, 2, 'ellipsis', 48159], metrics)).toBe(36 + 36 + 56 + 36 + 12 + 88);
    });

    it('charges a number past the table the widest width it has', () => {
        // A page cannot run longer than the last page, so this only says the
        // lookup stays defined rather than answering NaN to the fit.
        const metrics = { ...desktop1440, cells: [36, 40] };

        expect(pagerRowWidth([123], metrics)).toBe(40 + 88);
    });
});

describe('fitSiblingCount with cells by their own digits', () => {
    it('spends what short numbers saved on showing more of them', () => {
        const shared = fitSiblingCount(3, 48159, desktop1440, { boundaryCount: 2 });
        const own = fitSiblingCount(3, 48159, byDigits, { boundaryCount: 2 });

        expect(own).toBeGreaterThan(shared);
    });

    it('still fits the row at every depth of the catalogue', () => {
        for (const page of [1, 3, 9, 10, 99, 100, 1234, 9999, 10000, 40903, 48158, 48159]) {
            for (const row of [569, 748, 1064, 1200]) {
                const metrics = { ...byDigits, row };
                const siblings = fitSiblingCount(page, 48159, metrics, { boundaryCount: 2 });
                const items = paginationRange(page, 48159, {
                    boundaryCount: 2,
                    siblingCount: siblings,
                });

                if (siblings > 0) {
                    expect(pagerRowWidth(items, metrics)).toBeLessThanOrEqual(row);
                }
            }
        }
    });

    it('never searches below the true answer', () => {
        // The ceiling has to stay an upper bound now that cells differ: taken
        // from the widest cell it would stop the search short of a window of
        // short numbers that fits.
        const siblings = fitSiblingCount(3, 48159, byDigits, { boundaryCount: 2 });
        const wider = paginationRange(3, 48159, {
            boundaryCount: 2,
            siblingCount: siblings + 1,
        });

        expect(pagerRowWidth(wider, byDigits)).toBeGreaterThan(byDigits.row);
    });
});

describe('siblingCeiling', () => {
    it('bounds the search by what the row could hold at best', () => {
        // Each extra sibling adds two items, and no item is narrower than the
        // slimmest of a cell and an ellipsis. 1200px of row less both arrows
        // and their breathing holds at most 27 items of 36+4, and 27 items is
        // 13 siblings — a loose bound on the search, not the answer.
        expect(siblingCeiling(44500, desktop1440)).toBe(13);
    });

    it('never proposes more neighbours than the catalogue has pages', () => {
        // A five-page list in a 5000px row: without this the loop would count
        // down from a ceiling in the hundreds, rebuilding the range each step.
        expect(siblingCeiling(5, { ...desktop1440, row: 5000 })).toBe(3);
    });

    it('proposes nothing for a row with no room at all', () => {
        expect(siblingCeiling(44500, { ...desktop1440, row: 40 })).toBe(0);
    });
});

describe('fitSiblingCount on a desktop column', () => {
    it('opens the window past three when the column has the room', () => {
        // The old cap stopped at three however much room there was, which left
        // ~200px of air at either end of a 1200px column. Six siblings put
        // 19 elements in the row and measure 1150px of the 1200 — the browser
        // renders exactly those nineteen at 1440px.
        expect(fitSiblingCount(22000, 44500, desktop1440, { boundaryCount: 2 })).toBeGreaterThan(3);
    });

    it('stops where the measurement stops it, not at a constant', () => {
        const fitted = fitSiblingCount(22000, 44500, desktop1440, { boundaryCount: 2 });
        const items = paginationRange(22000, 44500, { boundaryCount: 2, siblingCount: fitted });
        const wider = paginationRange(22000, 44500, {
            boundaryCount: 2,
            siblingCount: fitted + 1,
        });

        expect(pagerRowWidth(items, desktop1440)).toBeLessThanOrEqual(desktop1440.row);
        expect(pagerRowWidth(wider, desktop1440)).toBeGreaterThan(desktop1440.row);
    });

    it('takes a narrower column down with it', () => {
        const wide = fitSiblingCount(22000, 44500, desktop1440, { boundaryCount: 2 });
        const narrow = fitSiblingCount(22000, 44500, desktop1024, { boundaryCount: 2 });

        expect(narrow).toBeLessThan(wide);
        expect(
            pagerRowWidth(
                paginationRange(22000, 44500, { boundaryCount: 2, siblingCount: narrow }),
                desktop1024,
            ),
        ).toBeLessThanOrEqual(desktop1024.row);
    });

    it('terminates on a short catalogue in an enormous row', () => {
        // Nothing measured stops the window here, so only the page count can.
        expect(fitSiblingCount(3, 5, { ...desktop1440, row: 5000 }, { boundaryCount: 2 })).toBe(3);
    });
});

describe('clampPage', () => {
    it('reads a number', () => {
        expect(clampPage('1234', 48159)).toBe(1234);
    });

    it('holds it to the list', () => {
        expect(clampPage('99999', 48159)).toBe(48159);
        expect(clampPage('0', 48159)).toBe(1);
    });

    it('takes the digits out of whatever a phone keyboard or a paste left', () => {
        expect(clampPage(' 12 345 ', 48159)).toBe(12345);
        expect(clampPage('12a3', 48159)).toBe(123);
    });

    it('survives more digits than a number can hold', () => {
        expect(clampPage('9'.repeat(400), 48159)).toBe(48159);
    });

    it('has no answer for nothing', () => {
        expect(clampPage('', 48159)).toBeNull();
        expect(clampPage('abc', 48159)).toBeNull();
    });
});

describe('pageBaseUrl', () => {
    it('drops the page number so siblings can be built from it', () => {
        expect(pageBaseUrl('/books/page/1')).toBe('/books/page');
        expect(pageBaseUrl('/books/find/category/42/7')).toBe('/books/find/category/42');
    });

    it('leaves a route that does not end in a page number alone', () => {
        expect(pageBaseUrl('/books/favorite')).toBe('/books/favorite');
    });

    it('keeps an id that is not the page number', () => {
        // The series id is numeric too; only the final segment is a page.
        expect(pageBaseUrl('/books/find/category/42')).toBe('/books/find/category');
    });
});

describe('pageHref', () => {
    it('replaces only the page number', () => {
        expect(pageHref('/books/page/5', 7)).toBe('/books/page/7');
        expect(pageHref('/books/find/category/42/3', 4)).toBe('/books/find/category/42/4');
    });

    it('carries the search over to the sibling page', () => {
        // The query string holds the search that filtered the list; dropping
        // it on page 2 would silently widen the results.
        expect(pageHref('/books/find/category/42/3?title=%D1%85&book_id=5', 2)).toBe(
            '/books/find/category/42/2?title=%D1%85&book_id=5',
        );
    });

    it('keeps the search on a scoped favourites list', () => {
        expect(pageHref('/books/favorite/2?title=%D1%85', 1)).toBe(
            '/books/favorite/1?title=%D1%85',
        );
    });
});

describe('pageHref on the addresses that page in the query', () => {
    it('puts a later page in the query', () => {
        expect(pageHref('/author/42', 3)).toBe('/author/42?page=3');
        expect(pageHref('/search?q=%D0%BA&type=authors', 2)).toBe(
            '/search?q=%D0%BA&type=authors&page=2',
        );
    });

    it('moves the page and keeps the rest of the query', () => {
        expect(pageHref('/author/42?title=%D1%85&page=3&book_id=5', 4)).toBe(
            '/author/42?title=%D1%85&page=4&book_id=5',
        );
    });

    it('writes no page for the first one', () => {
        expect(pageHref('/author/42?page=3', 1)).toBe('/author/42');
        expect(pageHref('/search?q=x&type=books&page=2', 1)).toBe('/search?q=x&type=books');
    });

    it('keeps a query made of punctuation readable on the next page', () => {
        const query = 'а/б?в%г+д';
        const href = pageHref(`/search?q=${encodeURIComponent(query)}&type=books`, 2);

        expect(new URLSearchParams(href.split('?')[1]).get('q')).toBe(query);
    });

    it('leaves the lists that page in the path as they are', () => {
        expect(pageHref('/books/find/genre/4/2?sort=author', 3)).toBe(
            '/books/find/genre/4/3?sort=author',
        );
        expect(pageHref('/admin/book-scanning/authors/3', 2)).toBe(
            '/admin/book-scanning/authors/2',
        );
    });
});
