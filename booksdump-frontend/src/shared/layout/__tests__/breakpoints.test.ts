import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { __unstable__loadDesignSystem } from 'tailwindcss';

import { CARD_WIDE_MIN_WIDTH_REM, CARD_WIDE_QUERY } from '@/shared/layout/breakpoints';

const projectPath = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));

/**
 * The card's one JS layout decision has to switch at the same moment its
 * responsive classes do. When it did not, the two disagreed across a band of
 * widths and the layout broke there and nowhere else.
 *
 * The width is neither restated here nor read out of a previous build. It is
 * compiled from the project's own stylesheet, so the answer belongs to the
 * source as it stands: a stale build directory cannot make this pass, and a
 * clean checkout does not need one. And it is compiled for the variant the
 * card actually writes, not for the smallest breakpoint in the application —
 * changing the card to `md:` while React kept asking for 40rem is exactly the
 * defect this is here to catch, and the whole-application minimum would have
 * stayed 40rem and said nothing.
 */
describe('the card layout boundary', () => {
    const cardSource = readFileSync(projectPath('../../../features/catalogue/BookCard.tsx'), 'utf8');

    /**
     * The responsive variants the card actually styles itself with.
     *
     * Comments are stripped first and only string literals are searched,
     * because a prefix mentioned in prose is not a style: with the classes
     * removed and the word left in a comment, an earlier version of this test
     * happily reported that the card still switched at 40rem.
     *
     * The prefix is captured whole, `max-` included. `max-sm:` is not a
     * spelling of `sm:` — it applies below the boundary rather than above it,
     * so pairing it with a min-width query puts CSS and React on opposite
     * sides of the same number.
     */
    const variantsUsedByCard = () => {
        const withoutComments = cardSource
            .replace(/\/\*[\s\S]*?\*\//g, ' ')
            .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ');
        const found = new Set<string>();
        for (const [, literal] of withoutComments.matchAll(/'([^'\n]*)'|"([^"\n]*)"/g)) {
            for (const [, variant] of (literal ?? '').matchAll(/(?:^|\s)((?:max-)?(?:sm|md|lg|xl|2xl)):/g)) {
                found.add(variant);
            }
        }
        return [...found];
    };

    const widthOfVariant = async (variant: string) => {
        const design = await __unstable__loadDesignSystem(
            readFileSync(projectPath('../../../index.css'), 'utf8'),
            {
                base: projectPath('../../../..'),
                loadStylesheet: async (id: string, base: string) => {
                    const path =
                        id === 'tailwindcss'
                            ? projectPath('../../../../node_modules/tailwindcss/index.css')
                            : '';
                    return { path, base, content: path ? readFileSync(path, 'utf8') : '' };
                },
            },
        );
        const [css] = design.candidatesToCss([`${variant}:block`]);
        const match = /width\s*>=\s*([\d.]+)rem/.exec(css ?? '');
        if (!match) {
            throw new Error(`the ${variant}: variant compiled to no width query: ${css}`);
        }
        return Number(match[1]);
    };

    it('is the width of the one responsive variant the card uses', async () => {
        const variants = variantsUsedByCard();
        expect(variants, 'the card styles nothing responsively, so nothing holds the boundary').not.toHaveLength(0);
        expect(variants, 'the card should style itself at one boundary, not several').toHaveLength(1);

        const [variant] = variants;
        expect(variant, 'a max-width variant applies below the boundary while the query asks about above it')
            .not.toMatch(/^max-/);

        await expect(widthOfVariant(variant)).resolves.toBe(CARD_WIDE_MIN_WIDTH_REM);
    });

    it('is asked in the unit the stylesheet uses, not in pixels', () => {
        // A pixel query agrees with a rem one only while the reader's font
        // size is 16px. Anyone who enlarged it would get the two apart again.
        expect(CARD_WIDE_QUERY).toBe(`(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`);
        expect(CARD_WIDE_QUERY).not.toMatch(/px/);
    });
});
