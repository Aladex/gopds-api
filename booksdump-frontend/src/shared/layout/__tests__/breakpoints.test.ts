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
     * Every class the card writes that carries a responsive variant.
     *
     * Comments are stripped first and only string literals are searched: with
     * the classes removed and the word left in prose, an earlier version of
     * this test happily reported that the card still switched at 40rem. Both
     * quote styles are collected — reading only single-quoted strings left
     * every className="..." in the file unexamined, which is most of them.
     *
     * Whole classes are kept, not prefixes. A variant chain like
     * `sm:max-lg:flex` starts with the right word and means something else
     * entirely, and no amount of prefix-matching notices that. What the class
     * means is decided by compiling it.
     */
    const responsiveClassesOfCard = () => {
        const withoutComments = cardSource
            .replace(/\/\*[\s\S]*?\*\//g, ' ')
            .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ');
        const responsive = /(?:^|:)(?:max-)?(?:sm|md|lg|xl|2xl):/;
        const found = new Set<string>();
        for (const match of withoutComments.matchAll(/'([^'\n]*)'|"([^"\n]*)"/g)) {
            const literal = match[1] ?? match[2] ?? '';
            for (const token of literal.split(/\s+/)) {
                if (responsive.test(token)) {
                    found.add(token);
                }
            }
        }
        return [...found];
    };

    let cached: Awaited<ReturnType<typeof __unstable__loadDesignSystem>> | undefined;

    /**
     * The project's own Tailwind, compiled in process from src/index.css.
     * Reading a previous build instead would answer for whatever was built
     * last, and there is nothing to build on a clean checkout.
     */
    const designSystem = async () => {
        cached ??= await __unstable__loadDesignSystem(
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
        return cached;
    };

    /** Every width condition the compiled class is guarded by. */
    const widthConditionsOf = async (candidate: string) => {
        const design = await designSystem();
        const [css] = design.candidatesToCss([candidate]);
        if (!css) {
            throw new Error(`${candidate} compiled to nothing`);
        }
        return [...css.matchAll(/width\s*(>=|<=|>|<)\s*([\d.]+)rem/g)].map((match) => ({
            operator: match[1],
            rem: Number(match[2]),
        }));
    };

    it('styles the card at exactly the boundary React asks about', async () => {
        const classes = responsiveClassesOfCard();
        expect(classes, 'the card styles nothing responsively, so nothing holds the boundary').not.toHaveLength(0);

        for (const candidate of classes) {
            const conditions = await widthConditionsOf(candidate);

            // One condition, not two: `sm:max-lg:` applies over a band and
            // leaves React wide past the top of it.
            expect(conditions, `${candidate} is guarded by ${conditions.length} width conditions`).toHaveLength(1);

            // Above the boundary, not below it: a max- variant is the other
            // side of the same number, and pairing it with a min-width query
            // puts CSS and React on opposite sides.
            expect(conditions[0].operator, `${candidate} applies below the boundary`).toBe('>=');

            expect(conditions[0].rem, `${candidate} switches at a different width`).toBe(CARD_WIDE_MIN_WIDTH_REM);
        }
    });

    it('is asked in the unit the stylesheet uses, not in pixels', () => {
        // A pixel query agrees with a rem one only while the reader's font
        // size is 16px. Anyone who enlarged it would get the two apart again.
        expect(CARD_WIDE_QUERY).toBe(`(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`);
        expect(CARD_WIDE_QUERY).not.toMatch(/px/);
    });
});
