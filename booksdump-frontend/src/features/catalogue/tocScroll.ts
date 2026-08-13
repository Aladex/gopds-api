/**
 * Where the contents column has to be scrolled for the marked chapter to be
 * visible in it.
 *
 * The column is sticky and scrolls by itself, so on a book of a hundred
 * chapters the marked one is routinely outside it: the highlight is correct
 * and the reader cannot see it.
 *
 * Split out as a function because the alternative is not testable. jsdom
 * reports every offset and height as zero, so an effect that reads the DOM
 * and assigns `scrollTop` can be verified only by a browser; the arithmetic
 * that decides *where* is the part worth holding, and here it is plain input
 * and output.
 *
 * `null` means leave the column alone. That is the common case — the marked
 * chapter is usually already in view, and scrolling on every crossing would
 * make the column twitch while the reader is not even looking at it.
 */
export function tocScrollTopFor(item: {
    /** Offset of the marked entry from the top of the scrolled content. */
    top: number;
    height: number;
    /** Current scroll offset of the column, and how much of it is visible. */
    viewTop: number;
    viewHeight: number;
}): number | null {
    const { top, height, viewTop, viewHeight } = item;
    // A column with no height of its own scrolls nowhere; this is also what
    // jsdom reports for everything, and answering "no move" there is more
    // honest than inventing an offset from zeroes.
    if (viewHeight <= 0) return null;

    const above = top < viewTop;
    const below = top + height > viewTop + viewHeight;
    if (!above && !below) return null;

    // Centred rather than just-inside: an entry scrolled to the very edge is
    // visible and still reads as "the list ends here", and the reader has no
    // sense of what comes next in the book.
    const centred = top - (viewHeight - height) / 2;
    return Math.max(0, centred);
}

/**
 * Which contents entry the reader is under, given where each anchor sits
 * relative to the top edge of the reading area.
 *
 * The last anchor at or above the edge, or -1 when the reader is above the
 * first one.
 *
 * This started life as bookkeeping over IntersectionObserver crossings, and
 * the browser threw it out. An anchor that scrolls from above the viewport to
 * below it in one jump — the reader dragging the bar from the end of the book
 * back to the start — changes no intersection ratio, so no crossing is
 * reported and the set of "passed" anchors keeps a chapter the reader left
 * long ago. Measured in Chrome: the highlight sat on chapter 7 all the way
 * back to the top of the book. The jsdom test passed throughout, because the
 * crossings it fed the component were the ones the component expected.
 *
 * Reading the positions instead cannot drift: whatever the reader did to get
 * there, the answer comes from where things are now.
 */
export function activeAnchorIndexFor(tops: readonly number[], edge: number): number {
    // A heading level with the top of the reading area is one the reader is
    // under, not one they are approaching, and "level" survives no rounding:
    // scrolling to a chapter leaves its anchor a fraction of a pixel below
    // where it was aimed. Measured in Chrome after jumping to chapter 6 — the
    // anchor at 149, the edge at 148, and the contents marking chapter 5.
    const line = edge + ANCHOR_ROUNDING_SLACK;
    let found = -1;
    for (let i = 0; i < tops.length; i++) {
        if (tops[i] <= line) found = i;
    }
    return found;
}

/**
 * How far below the top edge a heading may sit and still count as reached.
 * Small on purpose: this is for rounding, not for guessing what the reader
 * is looking at.
 */
export const ANCHOR_ROUNDING_SLACK = 4;
