import { describe, expect, it } from 'vitest';

import { TOC_COLUMN_CLASS } from '@/features/catalogue/BookPreviewDialog';
import { compileRules } from '@/shared/layout/tailwindProbe';

/**
 * The contents column beside the text has to stay put while the text scrolls,
 * and scroll by itself when the book has more chapters than the column is
 * tall.
 *
 * Reported from production: "оно тупо скроллится с текстом" — scrolling the
 * first chapter was enough to lose the contents off the top edge, on a book of
 * any length, because the column and the text share one scrolling ancestor.
 *
 * These are declarations, not rendered boxes: jsdom has no layout engine and
 * no scrolling, so nothing here can prove the column visibly stays. What it
 * proves is that the four declarations that make it stay are still on the
 * element, and that is what a refactor would silently drop.
 */
describe('the contents column stays with the reader', () => {
    const rules = compileRules(TOC_COLUMN_CLASS);

    const declaring = async (property: RegExp) =>
        (await rules).filter((rule) => property.test(rule.body));

    it('is stuck to the top of the work area', async () => {
        const position = await declaring(/position:/);
        expect(position).toHaveLength(1);
        expect(position[0].body).toMatch(/position:\s*sticky/);

        // Sticky with no offset never sticks to anything.
        const offset = await declaring(/(^|[^-])top:/);
        expect(offset).toHaveLength(1);
        expect(offset[0].body).toMatch(/top:\s*(0|0px)\b/);
    });

    it('does not stretch to the height of the text beside it', async () => {
        // The whole defect in one declaration. A flex child stretches to the
        // row by default, so the column's box already spans every screen of
        // the book and sticky has nothing left to hold: it rides up with the
        // text and passes this file's other assertions while doing it.
        const align = await declaring(/align-self:/);
        expect(align).toHaveLength(1);
        expect(align[0].body).toMatch(/align-self:\s*flex-start/);
    });

    it('scrolls by itself rather than growing past the work area', async () => {
        const maxHeight = await declaring(/max-height:/);
        expect(maxHeight).toHaveLength(1);
        expect(maxHeight[0].body).toMatch(/max-height:\s*100%/);

        const overflow = await declaring(/overflow-y:/);
        expect(overflow).toHaveLength(1);
        expect(overflow[0].body).toMatch(/overflow-y:\s*auto/);
    });
});
