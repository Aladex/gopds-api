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

    /** The responsive prefixes the card styles itself with. */
    const variantsUsedByCard = () => {
        const found = new Set<string>();
        for (const [, prefix] of cardSource.matchAll(/\b(sm|md|lg|xl|2xl):/g)) {
            found.add(prefix);
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
        expect(variants, 'the card should style itself at one boundary, not several').toHaveLength(1);
        await expect(widthOfVariant(variants[0])).resolves.toBe(CARD_WIDE_MIN_WIDTH_REM);
    });

    it('is asked in the unit the stylesheet uses, not in pixels', () => {
        // A pixel query agrees with a rem one only while the reader's font
        // size is 16px. Anyone who enlarged it would get the two apart again.
        expect(CARD_WIDE_QUERY).toBe(`(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`);
        expect(CARD_WIDE_QUERY).not.toMatch(/px/);
    });
});
