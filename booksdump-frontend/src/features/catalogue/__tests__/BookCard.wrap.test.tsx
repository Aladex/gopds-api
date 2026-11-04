import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard from '@/features/catalogue/BookCard';
import { fakeLayout, makeBook, renderCard, translate } from './bookCardHarness';

/*
 * On a wide card the annotation, once it passes the bottom of the action rail,
 * flows under the rail: a hidden float at the top of the details region
 * reserves the rail's footprint — its width is the rail column plus the gap
 * before it, its height is what hangs below the title block — and the text
 * wraps around it, newspaper-style. The rail itself stays where it is; only
 * the text's line boxes change. jsdom lays nothing out, so what is pinned
 * here is the contract the wrap rests on: the reservation exists only where
 * the rail hangs into the details, is invisible to readers and keyboards,
 * keeps out of the card's DOM order, follows the rail's height through the
 * shared measurement observer, and the details region is a block that spans
 * both text columns at the wide size.
 */

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: translate, i18n: { language: 'ru' } }),
}));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

const book = makeBook({
    annotation: 'Зона.',
    publisher: 'АСТ',
    genres: [{ id: 1, genre: 'Фантастика' }],
});

/** The rail column plus the gap before it, and the gap between the grid's rows. */
const RAIL_FOOTPRINT = 264;
const ROW_GAP = 8;

const spacer = (root: HTMLElement) => root.querySelector<HTMLElement>('[data-rail-spacer]');

const details = () =>
    document.getElementById(
        screen
            .getByRole('button', { name: /^(bookMore|bookLess)$/ })
            .getAttribute('aria-controls')!,
    )!;

/** A ResizeObserver that records itself and can be made to report. */
class RecordingObserver {
    static instances: RecordingObserver[] = [];
    observed = new Set<Element>();
    constructor(public callback: ResizeObserverCallback) {
        RecordingObserver.instances.push(this);
    }
    observe(element: Element) {
        this.observed.add(element);
    }
    unobserve(element: Element) {
        this.observed.delete(element);
    }
    disconnect() {
        this.observed.clear();
    }
    resize(elements: Element[]) {
        this.callback(
            elements.map((target) => ({ target }) as ResizeObserverEntry),
            this as unknown as ResizeObserver,
        );
    }
}

/*
 * Installed before the first render of the file: the measurement module
 * creates its one shared observer from whatever is installed at that moment,
 * so swapping the class later would change nothing.
 */
const originalObserver = globalThis.ResizeObserver;
beforeAll(() => {
    globalThis.ResizeObserver = RecordingObserver as unknown as typeof ResizeObserver;
});
afterAll(() => {
    globalThis.ResizeObserver = originalObserver;
});

let undo: (() => void) | undefined;
afterEach(() => {
    undo?.();
    undo = undefined;
});

describe('the rail’s reservation', () => {
    it('is a hidden float inside the details, as tall as the rail hangs below the title', () => {
        undo = fakeLayout({ railHeight: 320, titleHeight: 90 });
        const { container } = renderCard({ isWide: true, book });

        const node = spacer(container)!;
        expect(node).toHaveAttribute('aria-hidden', 'true');
        expect(node.style.width).toBe(`${RAIL_FOOTPRINT}px`);
        // 320 of rail, 90 of title, 8 of row gap between them and the details.
        expect(node.style.height).toBe(`${320 - 90 - ROW_GAP}px`);
        // A float, or the text would run straight through it.
        expect(node.className).toMatch(/\bfloat-right\b/);
    });

    it('is not there on a phone, whose actions sit under the text', () => {
        undo = fakeLayout({ railHeight: 320, titleHeight: 90 });
        const { container } = renderCard({ isWide: false, book });

        expect(spacer(container)).toBeNull();
    });

    it('is dropped when a wide card goes narrow, whatever the frozen measurements say', () => {
        undo = fakeLayout({ railHeight: 320, titleHeight: 90 });
        const view = renderCard({ isWide: true, book });
        expect(spacer(view.container)).not.toBeNull();

        // The window narrows past the wide card: the measurements stop (their
        // elements no longer reserve anything) but their last answer stands,
        // so the reservation must not survive on the narrow layout.
        view.rerender(
            <MemoryRouter>
                <BookCard
                    book={book}
                    isWide={false}
                    showLanguage={false}
                    isSuperuser={false}
                    formatDate={(value) => value}
                    isBookConverting={() => false}
                    onDownload={vi.fn()}
                    onPreview={vi.fn()}
                    onEpubRequest={vi.fn()}
                    onMobiRequest={vi.fn()}
                    onToggleFavourite={vi.fn()}
                    onToggleApproved={vi.fn()}
                    onRescan={vi.fn()}
                    onEdit={vi.fn()}
                />
            </MemoryRouter>,
        );

        expect(spacer(view.container)).toBeNull();
    });

    it('is not there when the rail ends above the details', () => {
        undo = fakeLayout({ railHeight: 96, titleHeight: 96 });
        const { container } = renderCard({ isWide: true, book });

        expect(spacer(container)).toBeNull();
    });
});

describe('what must not change', () => {
    it('keeps the card’s DOM order: title, annotation, actions', () => {
        undo = fakeLayout({ annotationClipped: true, railHeight: 320, titleHeight: 90 });
        renderCard({ isWide: true, book });

        const card = screen.getByTestId('book-card');
        const title = screen.getByRole('heading', { name: book.title });
        const annotation = screen.getByText('Зона.');
        const actions = screen.getByTestId('book-actions');
        const before = (a: Element, b: Element) =>
            (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;

        expect(before(title, annotation)).toBe(true);
        expect(before(annotation, actions)).toBe(true);
        // The reservation is a float, so it must precede the text it wraps —
        // but it is empty and aria-hidden, so it is in none of the orders a
        // reader, a keyboard or a screen reader follows.
        expect(before(spacer(card)!, annotation)).toBe(true);
        expect(screen.getByRole('button', { name: /^bookMore$/ })).toBeInTheDocument();
    });

    it('leaves the phone’s details block as it was', () => {
        undo = fakeLayout({});
        renderCard({ isWide: false, book });

        const names = details().className.split(/\s+/);
        expect(names).toContain('flex');
        expect(names).toContain('flex-col');
        expect(names).toContain('gap-2');
        expect(names).toContain('col-span-2');
    });
});

describe('the wide details region', () => {
    it('is a block spanning both text columns, so the text can use the rail’s empty space', () => {
        undo = fakeLayout({ annotationClipped: true, railHeight: 320, titleHeight: 90 });
        renderCard({ isWide: true, book });

        const names = details().className.split(/\s+/);
        expect(names).toContain('card:col-start-2');
        expect(names).toContain('col-span-2');
        // Flex ignores floats: the wide block flow is what lets the text wrap.
        expect(names).toContain('card:block');
        expect(names).not.toContain('card:col-span-1');
    });

    it('lets the under-rail lines run to the card’s right padding, uncapped', () => {
        undo = fakeLayout({ annotationClipped: true, railHeight: 320, titleHeight: 90 });
        renderCard({ isWide: true, book });

        // Andrey's circled case: the space under the rail is the point — a
        // max-width here, however chosen, keeps it empty. The right edge the
        // lines run to is the card's own, the same edge the rail sits on.
        expect(screen.getByText('Зона.').className).not.toMatch(/max-w-/);
    });
});

describe('keeping the reservation honest', () => {
    it('follows the rail’s height through the shared observer', async () => {
        undo = fakeLayout({ railHeight: 320, titleHeight: 90 });
        const { container } = renderCard({ isWide: true, book });
        await act(async () => {});
        expect(spacer(container)!.style.height).toBe(`${320 - 90 - ROW_GAP}px`);

        // The send block appears and the rail grows: the reservation must
        // follow, without any new observer for the card.
        undo();
        undo = fakeLayout({ railHeight: 380, titleHeight: 90 });
        const rail = container.querySelector<HTMLElement>('[data-measure="rail"]')!;
        act(() => RecordingObserver.instances[0].resize([rail]));

        expect(spacer(container)!.style.height).toBe(`${380 - 90 - ROW_GAP}px`);
        expect(RecordingObserver.instances).toHaveLength(1);
    });

    it('still opens through the height transition with the reservation in place', async () => {
        undo = fakeLayout({ annotationClipped: true, railHeight: 320, titleHeight: 90 });
        renderCard({ isWide: true, book });
        const block = details();
        expect(block.style.maxHeight).toBe('');

        await userEvent.click(screen.getByRole('button', { name: 'bookMore' }));
        await act(async () => {});

        expect(block.style.maxHeight).toMatch(/px$/);
        act(() => {
            block.dispatchEvent(
                Object.assign(new Event('transitionend', { bubbles: true }), {
                    propertyName: 'max-height',
                }),
            );
        });
        expect(block.style.maxHeight).toBe('');
    });
});

/*
 * The details region spans the rail's column (the wrap needs it there), and
 * its Expandable is a positioned box: painted after every in-flow block, it
 * sat over the rail and took the clicks of every rail button it overlapped —
 * FB2 to Telegram went dead on prod while still painted. jsdom does no
 * hit-testing, so what is pinned here is the stacking contract that keeps the
 * rail on top; the real-browser elementFromPoint check is in the fix report.
 */
describe('the rail above the details region', () => {
    const classes = (element: Element) => element.className.split(/\s+/);
    /** The element's z-index utility, if it has one: z-1, z-[1], -z-1, z-auto… */
    const zClass = (names: string[]) => names.find((name) => /^-?z-/.test(name));
    /**
     * The stacking level that utility sets: a number for z-N, z-[N] and their
     * negatives, NaN for anything else (z-auto, an arbitrary non-integer), so
     * only an explicit positive level passes a `> 0` check.
     */
    const zLevel = (names: string[]) => {
        const name = zClass(names);
        if (name === undefined) return undefined;
        const match = /^(-?)z-(?:\[(-?\d+)\]|(\d+))$/.exec(name);
        if (!match) return Number.NaN;
        const level = Number(match[2] ?? match[3]);
        return match[1] ? -level : level;
    };

    it('is a positioned layer above the region the annotation wraps in', () => {
        undo = fakeLayout({ annotationClipped: true, railHeight: 320, titleHeight: 90 });
        const { container } = renderCard({ isWide: true, book });

        const rail = classes(container.querySelector('[data-measure="rail"]')!);
        const region = classes(details());
        // The region is positioned (Expandable's own box), so the rail must
        // be too, and on a higher level — DOM order alone is not a contract.
        expect(region).toContain('relative');
        expect(rail).toContain('relative');
        // A positive level, not just any z utility: z-auto or a negative one
        // would leave the rail under the region again.
        expect(zLevel(rail)).toBeGreaterThan(0);
        expect(zClass(region)).toBeUndefined();
    });

    it('keeps that z-index inside the card', () => {
        undo = fakeLayout({ railHeight: 320, titleHeight: 90 });
        renderCard({ isWide: true, book });

        // Without its own stacking context the card's rail would compete with
        // the page chrome (the phone's tab bar, sticky headers) for the top.
        expect(classes(screen.getByTestId('book-card'))).toContain('isolate');
    });

    it('still takes a press on every button while the details are open', async () => {
        undo = fakeLayout({ annotationClipped: true, railHeight: 320, titleHeight: 90 });
        const handlers = {
            onPreview: vi.fn(),
            onDownload: vi.fn(),
            onEpubRequest: vi.fn(),
            onMobiRequest: vi.fn(),
            onSendToKindle: vi.fn(),
            onSendToTelegram: vi.fn(),
        };
        renderCard({
            isWide: true,
            book,
            showKindleButton: true,
            showTelegramButton: true,
            kindleConfigured: true,
            telegramConnected: true,
            ...handlers,
        });
        await userEvent.click(screen.getByRole('button', { name: 'bookMore' }));

        const rail = screen.getByTestId('book-actions');
        for (const button of within(rail).getAllByRole('button')) {
            await userEvent.click(button);
        }

        expect(handlers.onPreview).toHaveBeenCalledWith(book);
        expect(handlers.onDownload.mock.calls).toEqual([
            ['fb2', book.id],
            ['zip', book.id],
        ]);
        expect(handlers.onEpubRequest).toHaveBeenCalledWith(book.id);
        expect(handlers.onMobiRequest).toHaveBeenCalledWith(book.id);
        expect(handlers.onSendToKindle).toHaveBeenCalledWith(book);
        expect(handlers.onSendToTelegram).toHaveBeenCalledWith(book);
    });
});
