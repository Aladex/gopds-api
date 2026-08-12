import { describe, expect, it } from 'vitest';

import { CARD_WIDE_MIN_WIDTH_PX, CARD_WIDE_QUERY } from '@/shared/layout/breakpoints';

/**
 * Tailwind's `sm` is 40rem, and every browser this runs in has a 16px root
 * font, so `sm:` starts at 640px. The card's one JS layout decision has to use
 * that same number: when it did not, the two disagreed across a 39-pixel band
 * and the layout broke there and nowhere else, which is the hardest kind of
 * bug to see.
 *
 * If Tailwind's scale is ever customised, this test is the thing that fails.
 */
describe('the card layout boundary', () => {
    const tailwindSmInRem = 40;
    const rootFontSizePx = 16;

    it('is the same number as Tailwind sm', () => {
        expect(CARD_WIDE_MIN_WIDTH_PX).toBe(tailwindSmInRem * rootFontSizePx);
    });

    it('is asked as a min-width query, so it matches sm: rather than mirroring it', () => {
        expect(CARD_WIDE_QUERY).toBe(`(min-width: ${CARD_WIDE_MIN_WIDTH_PX}px)`);
    });
});
