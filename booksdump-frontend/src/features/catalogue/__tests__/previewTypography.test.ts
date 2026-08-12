import { describe, expect, it } from 'vitest';

import { TEXT_COLUMN_CLASS } from '@/features/catalogue/BookPreviewDialog';
import { compileClasses } from '@/shared/layout/tailwindProbe';

/**
 * The reading column's typography is a spec, not decoration, and a spec kept
 * only in a class list is one refactor away from gone. Nothing here reads the
 * class names: every assertion is made against the CSS the project's own
 * Tailwind compiles them to, so `text-[18px]`, `text-lg` and a theme token
 * that happens to equal 18px all answer the same question, and a rename that
 * changes the spelling without changing the type passes.
 *
 * What is not asserted here matters too. These are declarations, not rendered
 * boxes: jsdom has no layout engine, so "62 characters to the line" is held
 * as `max-width: 62ch` and the real measure belongs to the visual pass. The
 * value of pinning the declarations is that they cannot be lost silently.
 */
describe('the reading column carries the agreed typography', () => {
    const compiled = compileClasses(TEXT_COLUMN_CLASS);

    it('sets the measure at 62 characters', async () => {
        expect(await compiled).toMatch(/max-width:\s*62ch/);
    });

    it('sets 18px type on a 1.4 line', async () => {
        const css = await compiled;
        expect(css).toMatch(/font-size:\s*18px/);
        // 1.4 unitless, not 1.4rem and not a percentage: the line has to
        // scale with the type, including when a reader enlarges it.
        expect(css).toMatch(/line-height:\s*1\.4\b/);
        expect(css).not.toMatch(/line-height:\s*1\.4(rem|px|%)/);
    });

    it('indents paragraphs instead of spacing them apart', async () => {
        const css = await compiled;
        // A red line and a blank line are two ways to mark a paragraph, and
        // using both at once is the typographic error this pins: prose set
        // from FB2 gets the indent, and the margins go.
        expect(css).toMatch(/text-indent:\s*1\.5em/);
        expect(css).toMatch(/margin(-block)?:\s*0/);
    });

    it('justifies with hyphenation rather than torn word spacing', async () => {
        const css = await compiled;
        expect(css).toMatch(/text-align:\s*justify/);
        // Justification without hyphenation is what produces rivers of white
        // in a narrow column, so the two are asserted together on purpose.
        expect(css).toMatch(/hyphens:\s*auto/);
    });
});
