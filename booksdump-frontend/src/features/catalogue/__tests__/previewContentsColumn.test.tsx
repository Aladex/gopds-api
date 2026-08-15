import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

import BookPreviewDialog, { TOC_COLUMN_CLASS } from '@/features/catalogue/BookPreviewDialog';
import * as previewApi from '@/api/preview';
import { compileRules } from '@/shared/layout/tailwindProbe';

/**
 * The contents column beside the text has to stay put while the text scrolls,
 * and scroll by itself when the book has more chapters than the column is
 * tall.
 *
 * It used to do neither, in two rounds. First it rode up and off the top edge,
 * because it shared one scrolling ancestor with the text. Then it was made
 * `sticky` with `max-height: 100%` — and that resolves against the containing
 * block, which is as tall as the whole book. Measured in Chrome on a
 * 300-chapter book: a 7196px column inside a 682px work area, no scroll of
 * its own, every chapter past the first few unreachable.
 *
 * What fixed it is structural rather than a number: the column is a sibling
 * of the text's scroller, not a passenger inside it. That is what this file
 * holds, because it is the part a refactor would undo.
 */
vi.mock('@/api/preview', async () => {
    const actual = await vi.importActual<typeof import('@/api/preview')>('@/api/preview');
    return {
        ...actual,
        previewClient: { getPreview: vi.fn(), getChunk: vi.fn(), getImage: vi.fn() },
    };
});

const translate = (key: string) => key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));

// Wide layout: the column exists only there.
const matches = { current: true };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => matches.current,
}));

const getPreview = vi.mocked(previewApi.previewClient.getPreview);

describe('the contents column is not inside the text it accompanies', () => {
    it('sits beside the scrolling text, never within it', async () => {
        getPreview.mockResolvedValue({
            revision: 'rev-1',
            chunk_count: 1,
            toc: [
                { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
                { title: 'Chapter 2', depth: 1, chunk: 0, anchor: 'c2' },
            ],
            images: [],
            first_chunk: '<p data-testid="first-portion">first</p>',
        });

        render(<BookPreviewDialog open bookId={1} bookLang="ru" onClose={vi.fn()} />);
        await screen.findByTestId('first-portion');

        const column = screen.getByTestId('preview-toc');
        const scroller = screen.getByTestId('preview-scroll-area');

        // The whole fix in one assertion. Inside the scroller, the column has
        // no height to be bounded by — the scroller's content is the book —
        // so nothing can give it a scroll of its own, whatever CSS says.
        expect(scroller.contains(column)).toBe(false);

        // And the text is inside it, so "the scroll area" still means the
        // thing the reader scrolls to read.
        await waitFor(() =>
            expect(scroller.contains(screen.getByTestId('preview-text-column'))).toBe(true),
        );

        // Nor may anything above the column scroll instead. Being outside the
        // named scroller is not the property — having a height of its own is,
        // and any scrolling ancestor takes that away again. Adding an overflow
        // to the row passed the assertion above while putting the column right
        // back inside a scroller.
        const dialog = screen.getByRole('dialog');
        for (let node = column.parentElement; node && node !== dialog; node = node.parentElement) {
            expect(node.className).not.toMatch(/overflow-(y-)?(auto|scroll)/);
        }
    });
});

describe('the reader can see which chapter they are in', () => {
    it('marks the current entry visibly, not only for a screen reader', async () => {
        getPreview.mockResolvedValue({
            revision: 'rev-1',
            chunk_count: 1,
            toc: [
                { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
                { title: 'Chapter 2', depth: 1, chunk: 0, anchor: 'c2' },
            ],
            images: [],
            first_chunk: '<p data-testid="first-portion">first</p>',
        });

        render(<BookPreviewDialog open bookId={1} bookLang="ru" onClose={vi.fn()} />);
        await screen.findByTestId('first-portion');

        const column = screen.getByTestId('preview-toc');
        const [current, other] = [...column.querySelectorAll('button')];
        expect(current).toHaveAttribute('aria-current', 'page');

        // aria-current is the meaning and was all there was: the marked entry
        // looked exactly like every other one, so a sighted reader could not
        // tell where in the book they were. The marked row must differ from
        // an unmarked one by something an eye can see.
        const marked = current.className.split(/\s+/).filter(Boolean);
        const plain = other.className.split(/\s+/).filter(Boolean);
        const difference = marked.filter((one) => !plain.includes(one));
        expect(difference.length).toBeGreaterThan(0);

        // jsdom applies no stylesheet, so what is checked is that the marked
        // row asks for a different painting — the painting itself belongs to
        // the browser pass.
        expect(difference.join(' ')).toMatch(/border|bg-|font-/);
    });
});

describe('the contents column scrolls by itself', () => {
    const rules = compileRules(TOC_COLUMN_CLASS);

    const declaring = async (property: RegExp) =>
        (await rules).filter((rule) => property.test(rule.body));

    it('has its own overflow', async () => {
        const overflow = await declaring(/overflow-y:/);
        expect(overflow).toHaveLength(1);
        expect(overflow[0].body).toMatch(/overflow-y:\s*auto/);
    });

    it('does not carry a height of its own to be bounded by', async () => {
        // A max-height here would be a number standing in for the layout, and
        // the last one — `100%` — resolved against the book. The row above is
        // what bounds this column now; declaring a height again would be the
        // old mistake with a new value.
        expect(await declaring(/max-height:/)).toHaveLength(0);
        expect(await declaring(/position:\s*sticky/)).toHaveLength(0);
    });
});
