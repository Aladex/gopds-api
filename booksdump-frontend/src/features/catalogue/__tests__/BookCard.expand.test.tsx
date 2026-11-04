import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import { fakeLayout, makeBook, renderCard, translate } from './bookCardHarness';

/*
 * «Подробнее» opens the card through a height transition, not a class swap:
 * the details block is given a measured height to run between — the height of
 * the clamped content it is showing, and the height of everything it was
 * holding back — and the clamps themselves (three lines, one row of chips, the
 * truncated colophon) stay on until the motion is done. jsdom lays nothing
 * out, so what is pinned here is the same state machine the Expandable specs
 * pin: an explicit pixel pin first, a pixel target second, released at the
 * end, and nothing at all for a reader who asked for less motion.
 */

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: translate, i18n: { language: 'ru' } }),
}));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

const PEEK = '60px';
const FULL = '120px';

function setReducedMotion(reduce: boolean) {
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
        matches: query.includes('prefers-reduced-motion') ? reduce : false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia;
}

/** The block «Подробнее» opens, found the way the control names it. */
const details = () =>
    document.getElementById(
        screen
            .getByRole('button', { name: /^(bookMore|bookLess)$/ })
            .getAttribute('aria-controls')!,
    )!;

const annotation = () => screen.getByText('Зона.');

/**
 * recordMaxHeights collects every max-height the block is given, in order: the
 * pin that starts a transition is written and superseded within the same tick,
 * so the sequence is the only place it shows.
 */
function recordMaxHeights(el: HTMLElement): string[] {
    const maxHeightOf = (style: string | null) =>
        /max-height:\s*([^;]+)/.exec(style ?? '')?.[1].trim() ?? '';

    const seen: string[] = [];
    const observer = new MutationObserver((records) => {
        for (const record of records) seen.push(maxHeightOf(record.oldValue));
        seen.push(el.style.maxHeight);
    });
    observer.observe(el, {
        attributes: true,
        attributeFilter: ['style'],
        attributeOldValue: true,
    });
    return seen;
}

/** endTransition fires the event the block waits for. */
function endTransition(el: HTMLElement) {
    act(() => {
        el.dispatchEvent(
            Object.assign(new Event('transitionend', { bubbles: true }), {
                propertyName: 'max-height',
            }),
        );
    });
}

let undo: (() => void) | undefined;
beforeEach(() => setReducedMotion(false));
afterEach(() => {
    undo?.();
    undo = undefined;
});

const more = () => screen.getByRole('button', { name: /^(bookMore|bookLess)$/ });

const clipped = makeBook({
    annotation: 'Зона.',
    publisher: 'АСТ',
    isbn: ['978-5-17-177750-0'],
});

describe('opening the card', () => {
    it('runs between a measured pin and a measured target', async () => {
        undo = fakeLayout({ annotationClipped: true });
        renderCard({ isWide: true, book: clipped });
        const block = details();
        const seen = recordMaxHeights(block);

        await userEvent.click(more());
        await act(async () => {});

        // From the height on screen (the clamped annotation) to the height of
        // everything revealed — the two values a transition can run between.
        expect(seen.indexOf(PEEK)).toBeGreaterThanOrEqual(0);
        expect(seen.indexOf(FULL)).toBeGreaterThan(seen.indexOf(PEEK));
        // The clamp comes off as the height starts moving.
        expect(annotation().className).not.toMatch(/line-clamp/);
        expect(screen.getByTestId('book-card')).toHaveAttribute('data-state', 'open');

        endTransition(block);
        expect(block.style.maxHeight).toBe('');
    });

    it('carries the transition, and gives it up for reduced motion', () => {
        undo = fakeLayout({ annotationClipped: true });
        renderCard({ isWide: true, book: clipped });

        // The motion is CSS; these two classes are the whole of it.
        const names = details().className.split(/\s+/);
        expect(names).toContain('transition-[max-height]');
        expect(names).toContain('motion-reduce:transition-none');
    });

    it('swaps straight over when the reader asked for less motion', async () => {
        setReducedMotion(true);
        undo = fakeLayout({ annotationClipped: true });
        renderCard({ isWide: true, book: clipped });
        const seen = recordMaxHeights(details());

        await userEvent.click(more());
        await act(async () => {});

        // No measured heights at all: the content simply shows itself.
        expect(details().style.maxHeight).toBe('');
        expect(seen.filter((value) => /px$/.test(value))).toEqual([]);
        expect(annotation().className).not.toMatch(/line-clamp/);
    });
});

describe('shutting the card', () => {
    const openCard = async () => {
        undo = fakeLayout({ annotationClipped: true });
        renderCard({ isWide: true, book: clipped });
        await userEvent.click(more());
        await act(async () => {});
        const block = details();
        endTransition(block);
        return block;
    };

    it('pins the full height before closing onto the clamped one', async () => {
        const block = await openCard();
        expect(block.style.maxHeight).toBe('');
        const seen = recordMaxHeights(block);

        await userEvent.click(more());
        await act(async () => {});

        expect(seen.indexOf(FULL)).toBeGreaterThanOrEqual(0);
        expect(seen.indexOf(PEEK)).toBeGreaterThan(seen.indexOf(FULL));
        // While the height is still moving the text stays whole; the clamp
        // returns only once the motion is done.
        expect(annotation().className).not.toMatch(/line-clamp/);

        endTransition(block);
        expect(block.style.maxHeight).toBe('');
        expect(annotation().className).toMatch(/\bline-clamp-3\b/);
    });

    it('closes onto what the clamped content measures now, after the window moved', async () => {
        const block = await openCard();
        expect(block.style.maxHeight).toBe('');

        // The window narrows while the card is open: everything re-wraps, and
        // both the clamped and the full content get taller. The heights here
        // are handed to the element directly, as the harness does elsewhere.
        const WIDER_PEEK = '75px';
        const TALLER_FULL = '150px';
        Object.defineProperty(block, 'clientHeight', { configurable: true, get: () => 75 });
        Object.defineProperty(block, 'scrollHeight', { configurable: true, get: () => 150 });
        const seen = recordMaxHeights(block);

        await userEvent.click(more());
        await act(async () => {});

        // The pin is the full height as it is now, the target the clamped one
        // as it is now — not the pair the card measured when it opened.
        expect(seen.indexOf(TALLER_FULL)).toBeGreaterThanOrEqual(0);
        expect(seen.indexOf(WIDER_PEEK)).toBeGreaterThan(seen.indexOf(TALLER_FULL));

        endTransition(block);
        expect(block.style.maxHeight).toBe('');
        expect(annotation().className).toMatch(/\bline-clamp-3\b/);
    });

    it('keeps the control in place while the height is still moving', async () => {
        const block = await openCard();

        await userEvent.click(more());
        await act(async () => {});

        // The control has already said «Подробнее» again; taking it away
        // mid-motion because the open card's measurements say nothing is
        // hidden would be its own flicker.
        expect(more()).toHaveAccessibleName('bookMore');

        endTransition(block);
        expect(more()).toHaveAccessibleName('bookMore');
    });

    it('keeps a phone’s colophon until the motion is done', async () => {
        undo = fakeLayout({ annotationClipped: true });
        renderCard({ isWide: false, book: clipped });
        expect(screen.queryByTestId('book-colophon')).toBeNull();

        await userEvent.click(more());
        await act(async () => {});
        expect(screen.getByTestId('book-colophon')).toHaveTextContent(
            'АСТ · bookIsbn 978-5-17-177750-0',
        );

        const block = details();
        await userEvent.click(more());
        await act(async () => {});

        // Still mounted while the block shrinks onto it...
        expect(screen.queryByTestId('book-colophon')).not.toBeNull();
        endTransition(block);
        // ...and gone once the card is shut, as a phone keeps it for the
        // opened card only.
        expect(screen.queryByTestId('book-colophon')).toBeNull();
    });
});

describe('offering «Подробнее»', () => {
    it('still comes from the measurements of the shut card', async () => {
        undo = fakeLayout({});
        renderCard({ isWide: true, book: clipped });
        expect(screen.queryByRole('button', { name: /^(bookMore|bookLess)$/ })).toBeNull();

        undo();
        undo = fakeLayout({ annotationClipped: true });
        renderCard({ isWide: true, book: clipped });
        expect(more()).toHaveAccessibleName('bookMore');

        await userEvent.click(more());
        await act(async () => {});
        endTransition(details());

        // Open, it stays — as the way back.
        expect(more()).toHaveAccessibleName('bookLess');
        expect(within(details()).queryByRole('button', { name: /^bookGenresMore/ })).toBeNull();
    });
});
