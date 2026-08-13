import { describe, expect, it } from 'vitest';

import { activeAnchorIndexFor, tocScrollTopFor } from '@/features/catalogue/tocScroll';

/**
 * The contents column scrolls by itself, so the marked chapter can sit
 * outside it — correct and invisible, which is the same as useless on a book
 * of a hundred chapters.
 */
describe('bringing the marked chapter into the contents column', () => {
    const view = { viewTop: 100, viewHeight: 400 };

    it('leaves the column alone when the entry is already in view', () => {
        expect(tocScrollTopFor({ top: 200, height: 20, ...view })).toBeNull();
        // Flush against either edge still counts as in view: moving here
        // would mean the column twitches on almost every crossing.
        expect(tocScrollTopFor({ top: 100, height: 20, ...view })).toBeNull();
        expect(tocScrollTopFor({ top: 480, height: 20, ...view })).toBeNull();
    });

    it('centres an entry below the visible part', () => {
        // 900 is well past the bottom edge at 500.
        const top = tocScrollTopFor({ top: 900, height: 20, ...view });
        expect(top).toBe(900 - (400 - 20) / 2);
    });

    it('centres an entry above the visible part', () => {
        const top = tocScrollTopFor({ top: 40, height: 20, ...view });
        // Clamped at the start of the list rather than a negative offset.
        expect(top).toBe(0);
    });

    it('does not move a column that has no height to scroll', () => {
        // What jsdom reports for every element, and what a collapsed column
        // reports for real. Centring against a zero viewport would compute an
        // offset out of nothing.
        expect(
            tocScrollTopFor({ top: 900, height: 20, viewTop: 0, viewHeight: 0 }),
        ).toBeNull();
    });
});

describe('which chapter the reader is under', () => {
    const edge = 100;

    it('is the last heading at or above the top edge', () => {
        expect(activeAnchorIndexFor([-500, -200, 40, 800], edge)).toBe(2);
    });

    it('is nothing at all above the first heading', () => {
        // A book can open with matter before chapter 1 — a title page, an
        // epigraph — and claiming chapter 1 there would be a guess.
        expect(activeAnchorIndexFor([300, 900], edge)).toBe(-1);
    });

    it('gives the earlier chapter back when the reader scrolls up', () => {
        // The case the browser found and the observer version could not do:
        // dragging from the end of the book to the start left the highlight
        // on the chapter the reader had left, because nothing crossed an
        // edge on the way. Positions cannot drift like that.
        const atEnd = [-9000, -6000, -3000, -100];
        expect(activeAnchorIndexFor(atEnd, edge)).toBe(3);
        const backAtStart = [50, 3000, 6000, 9000];
        expect(activeAnchorIndexFor(backAtStart, edge)).toBe(0);
    });

    it('takes the last of several headings sharing a screen', () => {
        // Short chapters pack together; the reader is under the last one
        // they have gone past, not the first.
        expect(activeAnchorIndexFor([10, 20, 30, 500], edge)).toBe(2);
    });
});
