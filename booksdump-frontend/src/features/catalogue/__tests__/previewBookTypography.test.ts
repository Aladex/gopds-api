import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import postcss, { type Rule } from 'postcss';

/**
 * How the book itself is set: chapter openings, prose, verse, quotations and
 * footnotes.
 *
 * This markup comes from the server — its own elements and class names — so
 * it is styled in the stylesheet rather than in the dialog's class list, and
 * it is checked here by parsing that stylesheet. What a test can prove is
 * that the declarations exist and land on the right selector; that the result
 * reads well is a matter for eyes and a browser.
 *
 * The starting point was measured, not guessed: a chapter heading computed to
 * 18px at weight 400 — the body text exactly — because the framework's reset
 * flattens h1..h6 and nothing had put them back. A reader's words for the
 * whole class of defect: "сноски смешиваются с текстом, главы не выделены".
 */
describe('the book is set like a book', () => {
    const css = readFileSync(resolve(process.cwd(), 'src/index.css'), 'utf8');
    const root = postcss.parse(css);

    /** Declarations of `property` on rules whose selector matches. */
    const declared = (property: string, selector: RegExp) => {
        const found: { selector: string; value: string }[] = [];
        root.walkRules((rule: Rule) => {
            for (const one of rule.selectors) {
                if (!selector.test(one)) continue;
                rule.walkDecls(property, (decl) => {
                    found.push({ selector: one, value: decl.value.trim() });
                });
            }
        });
        return found;
    };

    it('marks a chapter opening apart from the text', () => {
        // Both, and on the headings themselves: size alone at body weight is
        // what a reset leaves behind, and weight alone at body size reads as
        // an emphasised sentence rather than a new chapter.
        const headings = /\.portion\s.*h1/;
        expect(declared('font-weight', headings).length).toBeGreaterThan(0);
        expect(declared('font-size', headings).length).toBeGreaterThan(0);

        // And room around it. A heading flush against the paragraph above is
        // a line in bold, not a division of the book.
        const spacing = declared('margin-block', headings);
        expect(spacing.length).toBeGreaterThan(0);
        expect(spacing.some((d) => d.value !== '0')).toBe(true);
    });

    it('indents prose paragraphs and does not also space them apart', () => {
        const indent = declared('text-indent', /\.portion p$/);
        expect(indent).toHaveLength(1);
        expect(indent[0].value).toMatch(/1\.5em/);

        const margin = declared('margin-block', /\.portion p$/);
        expect(margin).toHaveLength(1);
        expect(margin[0].value).toBe('0');
    });

    it('does not indent the paragraph that opens a section', () => {
        // Nothing above it to be indented away from; the indent there reads
        // as an accident.
        const after = declared('text-indent', /h1, h2, h3, h4, h5, h6\) \+ p/);
        expect(after).toHaveLength(1);
        expect(after[0].value).toBe('0');
    });

    it('reaches the first heading past the anchor the renderer puts before it', () => {
        // The renderer emits an empty <a id="…"> ahead of every heading it
        // anchors, so a heading is almost never its portion's first child and
        // a plain :first-child rule fires for nothing. This was written that
        // way and did nothing at all until it was pointed out.
        const rules: string[] = [];
        root.walkRules((rule: Rule) => {
            const hasReset = rule.nodes?.some(
                (node) =>
                    node.type === 'decl' &&
                    node.prop === 'margin-block-start' &&
                    node.value.trim() === '0',
            );
            if (!hasReset) return;
            rules.push(...rule.selectors);
        });
        expect(rules.some((one) => /a:first-child \+ :is\(h1/.test(one))).toBe(true);
    });

    it('raises only footnote references, not every link into the book', () => {
        // A note's anchor is pv{chunk}-note-{key}; an ordinary cross-
        // reference is pv-{key}, and both start with #pv. Matching the prefix
        // alone set "see chapter II" as a tiny superscript.
        const raised: string[] = [];
        root.walkRules((rule: Rule) => {
            const isRaised = rule.nodes?.some(
                (node) =>
                    node.type === 'decl' &&
                    node.prop === 'vertical-align' &&
                    node.value.trim() === 'super',
            );
            if (!isRaised) return;
            raised.push(...rule.selectors.filter((one) => one.includes('a[href')));
        });
        expect(raised.length).toBeGreaterThan(0);
        for (const selector of raised) {
            expect(selector).toContain('-note-');
        }
    });

    it('keeps a wide table inside the column instead of on the page', () => {
        // Unstyled, a table wider than the reading column pushes the whole
        // page sideways on a phone. It scrolls in its own box instead.
        expect(declared('overflow-x', /\.portion \.table$/)[0]?.value).toBe('auto');
        expect(declared('max-width', /\.portion \.table$/)[0]?.value).toBe('100%');
        expect(declared('border', /\.portion \.table :is\(td, th\)/).length).toBeGreaterThan(0);
    });

    it('sets verse as the poet broke it', () => {
        // No paragraph indent, no justification, no hyphenation: a line of
        // verse was measured to fit and must not be stretched or broken. The
        // first version of this styling missed it — the rule said "every <p>"
        // and lines of verse are paragraphs.
        expect(declared('text-indent', /\.poem-line/)[0]?.value).toBe('0');
        expect(declared('text-align', /\.poem-line/)[0]?.value).toBe('left');
        expect(declared('hyphens', /\.poem-line/)[0]?.value).toBe('none');
    });

    it('gives a footnote a voice of its own', () => {
        // Opened in place, an unstyled note simply lengthens the paragraph it
        // interrupts. It needs to be set apart by more than position.
        const note = /\.preview-note$/;
        expect(declared('font-size', note).length).toBeGreaterThan(0);
        expect(declared('border-inline-start', note).length).toBeGreaterThan(0);
        expect(declared('padding', note).length).toBeGreaterThan(0);

        // And its paragraphs lose the prose indent: a note is not prose.
        expect(declared('text-indent', /\.preview-note p/)[0]?.value).toBe('0');
    });

    it('raises the reference in the text', () => {
        const ref = /\.portion a\[href/;
        expect(declared('vertical-align', ref)[0]?.value).toBe('super');
        expect(declared('font-size', ref).length).toBeGreaterThan(0);
    });

    it('raises superscripts and drops subscripts', () => {
        // The renderer emits <sup> and <sub>, and the framework's reset puts
        // both back on the baseline: measured in Chrome, vertical-align on a
        // superscript computed to `baseline`, so an ordinal or a formula was
        // set as ordinary text. Nothing in the book fixtures had one, which
        // is why it took a browser and a hand-written probe to notice.
        expect(declared('vertical-align', /\.portion sup/)[0]?.value).toBe('super');
        expect(declared('vertical-align', /\.portion sub/)[0]?.value).toBe('sub');
        expect(declared('font-size', /\.portion :is\(sup, sub\)/).length).toBeGreaterThan(0);
    });

    it('justifies the running text, with hyphenation to pay for it', () => {
        // Justification without hyphenation is what tears rivers of white
        // through a narrow column, so the two are asserted together.
        const portion = /^\.portion$/;
        expect(declared('text-align', portion)[0]?.value).toBe('justify');
        expect(declared('hyphens', portion)[0]?.value).toBe('auto');
    });
});
