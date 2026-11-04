import { act, screen, within } from '@testing-library/react';

import { fakeLayout, makeBook, renderCard, translate } from './bookCardHarness';

/*
 * A card measures up to three things (the annotation, the genre row, the
 * colophon) to decide whether to offer «Подробнее», and a page holds fifty
 * cards. They share one ResizeObserver and one wait for the fonts, rather
 * than holding one each — and a resize still reaches the card it happened to.
 */

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: translate, i18n: { language: 'ru' } }),
}));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

/** A ResizeObserver that records itself and can be made to report. */
class RecordingObserver {
    static instances: RecordingObserver[] = [];
    observed = new Set<Element>();
    unobserved: Element[] = [];
    constructor(public callback: ResizeObserverCallback) {
        RecordingObserver.instances.push(this);
    }
    observe(element: Element) {
        this.observed.add(element);
    }
    unobserve(element: Element) {
        this.observed.delete(element);
        this.unobserved.push(element);
    }
    disconnect() {
        this.observed.clear();
    }
    /** Reports a resize of the given elements. */
    resize(elements: Element[]) {
        this.callback(
            elements.map((target) => ({ target }) as ResizeObserverEntry),
            this as unknown as ResizeObserver,
        );
    }
}

let fontsWaits = 0;
const originalObserver = globalThis.ResizeObserver;
const originalFonts = Object.getOwnPropertyDescriptor(document, 'fonts');

beforeAll(() => {
    globalThis.ResizeObserver = RecordingObserver as unknown as typeof ResizeObserver;
    const ready = Promise.resolve();
    Object.defineProperty(document, 'fonts', {
        configurable: true,
        value: {
            get ready() {
                return {
                    then: (resolve: () => void) => {
                        fontsWaits += 1;
                        return ready.then(resolve);
                    },
                };
            },
        },
    });
});

afterAll(() => {
    globalThis.ResizeObserver = originalObserver;
    if (originalFonts) Object.defineProperty(document, 'fonts', originalFonts);
    else delete (document as unknown as Record<string, unknown>).fonts;
});

let undo: () => void = () => {};
afterEach(() => undo());

const book = (id: number) =>
    makeBook({
        id,
        title: `Книга ${id}`,
        annotation: `Аннотация ${id}.`,
        publisher: 'АСТ',
        genres: [
            { id: 1, genre: 'Фантастика' },
            { id: 2, genre: 'Повесть' },
        ],
    });

/** The cards of the current render, by the book's title. */
const cardOf = (title: string) =>
    screen.getByRole('heading', { name: title }).closest<HTMLElement>('article')!;

describe('the cards of a page', () => {
    it('share one observer and one wait for the fonts, however many there are', async () => {
        undo = fakeLayout({});
        const views = [1, 2, 3, 4, 5].map((id) => renderCard({ isWide: true, book: book(id) }));
        await act(async () => {});

        expect(RecordingObserver.instances).toHaveLength(1);
        // Every card's annotation, genre row and colophon are watched.
        expect(RecordingObserver.instances[0].observed.size).toBe(15);
        expect(fontsWaits).toBe(1);

        views.forEach((view) => view.unmount());
        expect(RecordingObserver.instances[0].observed.size).toBe(0);
    });

    it('re-measure the card that was resized', async () => {
        undo = fakeLayout({});
        renderCard({ isWide: true, book: book(6) });
        renderCard({ isWide: true, book: book(7) });
        const more = (title: string) =>
            within(cardOf(title)).queryByRole('button', { name: 'bookMore' });
        expect(more('Книга 6')).toBeNull();

        // The window narrows: the annotation of the sixth no longer fits.
        undo();
        undo = fakeLayout({ annotationClipped: true });
        const annotation = within(cardOf('Книга 6')).getByText('Аннотация 6.');
        act(() => RecordingObserver.instances[0].resize([annotation]));

        expect(more('Книга 6')).not.toBeNull();
        // Nothing reported the seventh resized, so it was not asked again.
        expect(more('Книга 7')).toBeNull();
    });
});
