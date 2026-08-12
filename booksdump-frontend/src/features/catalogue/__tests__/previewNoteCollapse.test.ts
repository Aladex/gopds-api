import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import postcss from 'postcss';

/**
 * A portion arrives with every footnote inlined under the paragraph that
 * cites it, and the dialog collapses them after the render commits. Between
 * those two moments there is one painted frame in which a chapter with four
 * hundred notes is drawn at full height and then snaps shut — on every
 * "Next", not only the first. A stylesheet rule hides a note until the dialog
 * has marked it as handled, which removes the frame without depending on when
 * an effect runs.
 *
 * jsdom paints nothing, so no rendering test can see that frame, and deleting
 * the rule left the whole suite green. This is the honest substitute: it
 * proves the shipped stylesheet still carries the rule, and nothing more. It
 * does not prove the reader sees no flash — that belongs to a browser.
 *
 * The stylesheet is parsed rather than searched as text, so whitespace,
 * ordering and the layer it sits in are free to change.
 */
describe('the stylesheet hides a footnote until the dialog has handled it', () => {
    // Resolved from the project root rather than from import.meta.url: under
    // the test runner this module's URL is not a file: one.
    const css = readFileSync(resolve(process.cwd(), 'src/index.css'), 'utf8');

    it('carries a rule that collapses an unhandled note', () => {
        const root = postcss.parse(css);
        const hiding: string[] = [];

        root.walkRules((rule) => {
            const hidesIt = rule.nodes?.some(
                (node) =>
                    node.type === 'decl' &&
                    node.prop === 'display' &&
                    node.value.trim() === 'none',
            );
            if (!hidesIt) return;
            // The selector has to name a note that has *not* been marked. A
            // rule hiding `.preview-note` outright would pass a substring
            // check and hide the reader's opened note forever.
            for (const selector of rule.selectors) {
                if (!selector.includes('.preview-note')) continue;
                if (!selector.includes(':not([data-preview-note-init])')) continue;
                hiding.push(selector);
            }
        });

        expect(hiding).toHaveLength(1);
    });
});
