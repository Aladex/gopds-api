import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { CARD_WIDE_MIN_WIDTH_PX, CARD_WIDE_QUERY } from '@/shared/layout/breakpoints';

const source = (relative: string) =>
    readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8');

/**
 * The card used to carry two layout boundaries: the list asked useMediaQuery
 * for 600px while the card branched on `sm:` at 640px. Between 601 and 639 the
 * two disagreed and the download block landed in a container built for the
 * other layout — a bug visible in a 39-pixel band and nowhere else.
 *
 * What has to hold now is not "at width X the layout is Y": jsdom applies no
 * stylesheet, so no rendered test can observe a Tailwind breakpoint at all. A
 * test that claimed to compare CSS against JS in this environment would be
 * comparing nothing. What is observable, and what actually prevents the bug,
 * is that only one number exists and the list asks for that one.
 */
describe('the card has a single layout boundary', () => {
    const listSource = source('../BooksList.tsx');
    const cardSource = source('../BookCard.tsx');

    it('is asked for by the list through the shared query', () => {
        expect(listSource).toContain('useMediaQuery(CARD_WIDE_QUERY)');
    });

    it('has no second threshold written anywhere in the card or the list', () => {
        // The old pair. Either spelling reappearing means the boundary has
        // been forked again, whatever the number happens to be.
        for (const forbidden of ['max-width: 600px', 'min-width: 600px', '600px']) {
            expect(listSource).not.toContain(forbidden);
            expect(cardSource).not.toContain(forbidden);
        }
    });

    it('agrees with the Tailwind breakpoint the card styles with', () => {
        // Every `sm:` class in the card switches at this width. If the shared
        // constant ever drifts from it, the card is back to two boundaries.
        expect(CARD_WIDE_MIN_WIDTH_PX).toBe(640);
        expect(CARD_WIDE_QUERY).toContain('640');
        expect(cardSource).toContain('sm:');
    });

    it('makes exactly one layout decision in JS, and the card takes it as a flag', () => {
        // One prop, one meaning. The card must not re-derive width itself:
        // a second query inside the card is a second boundary by another name.
        expect(cardSource).toContain('isWide');
        expect(cardSource).not.toContain('useMediaQuery');
    });
});
