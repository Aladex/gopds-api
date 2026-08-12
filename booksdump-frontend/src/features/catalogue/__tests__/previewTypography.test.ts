import { describe, expect, it } from 'vitest';

import { SCROLL_AREA_CLASS, TEXT_COLUMN_CLASS } from '@/features/catalogue/BookPreviewDialog';
import { compileRules, type CompiledRule } from '@/shared/layout/tailwindProbe';

/**
 * The reading column's typography is a spec, not decoration, and a spec kept
 * only in a class list is one refactor away from gone. Nothing here reads the
 * class names: every assertion is made against the CSS the project's own
 * Tailwind compiles them to, so `text-[18px]`, `text-lg` and a theme token
 * that happens to equal 18px all answer the same question, and a rename that
 * changes the spelling without changing the type passes.
 *
 * Each declaration is also checked against the selector it lands on. An
 * earlier version of this test joined the whole compiled stylesheet into one
 * string and searched it, which cannot tell "the column is 62 characters
 * wide" from "something in here is 62 characters wide": moving the indent
 * from paragraphs onto headings, or the justification onto the contents list,
 * left it green.
 *
 * What is not asserted matters too. These are declarations, not rendered
 * boxes: jsdom has no layout engine, so "62 characters to the line" is held
 * as `max-width: 62ch` and the real measure belongs to the visual pass.
 */
describe('the reading column carries the agreed typography', () => {
    const rules = compileRules(TEXT_COLUMN_CLASS);

    /**
     * The rules declaring a property, and where each one applies. `scope` is
     * what the selector reaches past the class itself: '' for the column, and
     * a descendant like '.portion p' for the prose inside it.
     */
    const declaring = async (property: RegExp) => {
        const found = (await rules).filter((rule: CompiledRule) => property.test(rule.body));
        return found.map((rule) => ({
            candidate: rule.candidate,
            // The class part of the selector is escaped beyond recognition
            // (`.max-w-\[62ch\]`), so the scope is read as "whatever follows
            // the first selector token" rather than by matching the name.
            scope: rule.selector.split(/\s+/).slice(1).join(' '),
            body: rule.body,
        }));
    };

    it('sets the measure at 62 characters, on the column itself', async () => {
        const found = await declaring(/max-width:/);
        expect(found).toHaveLength(1);
        expect(found[0].body).toMatch(/max-width:\s*62ch/);
        // On the element carrying the class, not on something nested inside
        // it: a 62ch box wrapped in a wider one is not a 62ch measure.
        expect(found[0].scope).toBe('');
    });

    it('sets 18px type on a 1.4 line, on the column itself', async () => {
        const size = await declaring(/font-size:/);
        expect(size).toHaveLength(1);
        expect(size[0].body).toMatch(/font-size:\s*18px/);
        expect(size[0].scope).toBe('');

        const line = await declaring(/line-height:/);
        expect(line).toHaveLength(1);
        // Unitless, not 1.4rem and not a percentage: the line has to scale
        // with the type, including when a reader enlarges it.
        expect(line[0].body).toMatch(/line-height:\s*1\.4\b/);
        expect(line[0].body).not.toMatch(/line-height:\s*1\.4(rem|px|%)/);
        expect(line[0].scope).toBe('');
    });

    it('indents the prose paragraphs, and only those', async () => {
        const indent = await declaring(/text-indent:/);
        expect(indent).toHaveLength(1);
        expect(indent[0].body).toMatch(/text-indent:\s*1\.5em/);
        // Paragraphs of the book, not headings and not the dialog's own text.
        expect(indent[0].scope).toBe('.portion p');

        // A red line and a blank line are two ways to mark a paragraph, and
        // using both at once is the typographic error this pins.
        const margin = await declaring(/margin(-block)?:/);
        expect(margin).toHaveLength(1);
        expect(margin[0].body).toMatch(/margin(-block)?:\s*0/);
        expect(margin[0].scope).toBe('.portion p');
    });

    it('justifies the book text with hyphenation, and nothing else', async () => {
        const align = await declaring(/text-align:/);
        expect(align).toHaveLength(1);
        expect(align[0].body).toMatch(/text-align:\s*justify/);
        expect(align[0].scope).toBe('.portion');

        // Justification without hyphenation is what produces rivers of white
        // in a narrow column, so the two are asserted together on purpose —
        // and on the same scope, or one of them is not doing its half.
        const hyphens = await declaring(/hyphens:/);
        expect(hyphens).toHaveLength(1);
        expect(hyphens[0].body).toMatch(/hyphens:\s*auto/);
        expect(hyphens[0].scope).toBe('.portion');
    });

    it('centres the column in the work area', async () => {
        // `flex-1` fills the space; the auto margins are what put the leftover
        // on both sides of the text instead of all of it on the right.
        const auto = await declaring(/margin-inline:\s*auto/);
        expect(auto).toHaveLength(1);
        expect(auto[0].scope).toBe('');
    });
});

describe('the work area reserves room for the scrollbar', () => {
    it('keeps the gutter stable on both edges', async () => {
        // Otherwise the bar takes its width from one side and the centred
        // column stops being centred the moment a portion is long enough to
        // scroll — 24px against 39px when this was measured on the mockup.
        const rules = await compileRules(SCROLL_AREA_CLASS);
        const gutter = rules.filter((rule) => /scrollbar-gutter:/.test(rule.body));
        expect(gutter).toHaveLength(1);
        expect(gutter[0].body).toMatch(/scrollbar-gutter:\s*stable both-edges/);
    });
});
