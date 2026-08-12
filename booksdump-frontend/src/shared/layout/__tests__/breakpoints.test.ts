import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { CARD_WIDE_MIN_WIDTH_REM, CARD_WIDE_QUERY } from '@/shared/layout/breakpoints';

const projectFile = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));

/**
 * The card's one JS layout decision has to switch at the same moment its `sm:`
 * classes do. When it did not, the two disagreed across a band of widths and
 * the layout broke there and nowhere else — the hardest kind of bug to see.
 *
 * The number is not restated here. It is read out of the stylesheet the build
 * actually produces, because that is the artefact the browser obeys: writing
 * `40 × 16` in the test would only prove that the test and the constant were
 * typed by the same hand.
 */
describe('the card layout boundary', () => {
    const builtStylesheet = () => {
        const dir = projectFile('../../../../build/assets');
        const css = readdirSync(dir).filter((name) => name.endsWith('.css'));
        if (css.length === 0) {
            throw new Error('no built stylesheet in build/assets — run the build first');
        }
        return css.map((name) => readFileSync(`${dir}/${name}`, 'utf8')).join('\n');
    };

    it('is the first width Tailwind switches at in the built stylesheet', () => {
        const widths = [...builtStylesheet().matchAll(/width>=([\d.]+)rem/g)]
            .map((match) => Number(match[1]))
            .sort((a, b) => a - b);

        expect(widths.length).toBeGreaterThan(0);
        expect(widths[0]).toBe(CARD_WIDE_MIN_WIDTH_REM);
    });

    it('is asked in the unit the stylesheet uses, not in pixels', () => {
        // A pixel query agrees with a rem one only while the reader's font
        // size is 16px. Anyone who enlarged it would get the two apart again.
        expect(CARD_WIDE_QUERY).toBe(`(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`);
        expect(CARD_WIDE_QUERY).not.toMatch(/px/);
    });
});
