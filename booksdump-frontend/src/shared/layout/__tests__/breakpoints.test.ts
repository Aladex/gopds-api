import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { __unstable__loadDesignSystem } from 'tailwindcss';
import { Scanner } from '@tailwindcss/oxide';

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

    /**
     * Every class the card writes, found the way Tailwind finds them.
     *
     * Earlier versions of this test picked candidates out of the source with
     * regexes of my own, and each missed a way of writing a class that still
     * reached the stylesheet: strings in double quotes, template literals,
     * a variant spelled `min-[48rem]:` rather than `sm:`. The scanner below is
     * the one Tailwind uses to decide what a file asks for, so anything the
     * build compiles is something this test sees.
     */
    const cardCandidates = () =>
        new Scanner({}).scanFiles([{ content: cardSource, extension: 'tsx' }]);

    it('styles the card at exactly the boundary React asks about', async () => {
        const design = await designSystem();
        const guarded: string[] = [];

        for (const candidate of cardCandidates()) {
            const [css] = design.candidatesToCss([candidate]);
            if (!css || !/width\s*[<>]=?/.test(css)) {
                continue; // not a width-dependent class at all
            }
            guarded.push(candidate);

            // Exactly one variant, asked of Tailwind's own parser rather than
            // recognised from a list of spellings. dark:sm:, active:sm:,
            // supports-[…]:sm: and sm:max-lg: are all the same defect — the
            // layout answering to something besides the width — and listing
            // them one by one only ever catches the ones already thought of.
            const [parsed] = [...design.parseCandidate(candidate)];
            expect(parsed, `${candidate} does not parse`).toBeDefined();
            expect(parsed.variants, `${candidate} is conditional on more than a width`).toHaveLength(1);

            // A viewport width, not a container's. @min-[40rem]: carries the
            // right number and answers to the size of an ancestor, which is
            // not what matchMedia is watching.
            expect(css, `${candidate} responds to a container, not the viewport`).not.toMatch(/@container/);
            expect(css, `${candidate} is not a media query`).toMatch(/@media/);

            const conditions = [...css.matchAll(/width\s*(>=|<=|>|<)\s*([\d.]+)rem/g)];
            expect(conditions, `${candidate} is guarded by ${conditions.length} width conditions`).toHaveLength(1);

            // Above the boundary, not below: max-sm: is the other side of the
            // same number, and pairing it with a min-width query puts CSS and
            // React on opposite sides.
            expect(conditions[0][1], `${candidate} applies below the boundary`).toBe('>=');
            expect(Number(conditions[0][2]), `${candidate} switches at another width`)
                .toBe(CARD_WIDE_MIN_WIDTH_REM);
        }

        expect(guarded, 'the card styles nothing by width, so nothing holds the boundary').not.toHaveLength(0);
    });

    it('is asked in the unit the stylesheet uses, not in pixels', () => {
        // A pixel query agrees with a rem one only while the reader's font
        // size is 16px. Anyone who enlarged it would get the two apart again.
        expect(CARD_WIDE_QUERY).toBe(`(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`);
        expect(CARD_WIDE_QUERY).not.toMatch(/px/);
    });
});
