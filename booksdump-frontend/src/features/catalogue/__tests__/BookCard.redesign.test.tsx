import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import type { AuthorDisplay, Book } from '@/api/books';
import { fakeLayout, makeBook, renderCard, translate } from './bookCardHarness';

/*
 * The redesigned card: a prose byline with a counter, chips, a colophon, an
 * annotation clamped to three lines, the star on the cover, and the actions
 * as a rail (wide) or as a row of three verbs with bottom sheets (narrow).
 */

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: translate, i18n: { language: 'ru' } }),
}));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
const setAuthorName = vi.fn();
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName }) }));

beforeAll(() => {
    // vaul's sheet captures the pointer on press; jsdom has no such API.
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
});

beforeEach(() => vi.clearAllMocks());

const actions = () => screen.getByTestId('book-actions');
const buttonNames = (root: HTMLElement) =>
    within(root)
        .getAllByRole('button')
        .map((button) => button.getAttribute('aria-label') ?? button.textContent);

const KINDLE = /^sendToKindle\.(send|setup|sending)$/;
const TELEGRAM = /^sendToTelegram\.(send|setup|sending)$/;

describe('the send rule', () => {
    const cases = [
        { kindle: false, telegram: false, label: 'both off' },
        { kindle: true, telegram: false, label: 'Kindle only' },
        { kindle: false, telegram: true, label: 'Telegram only' },
        { kindle: true, telegram: true, label: 'both on' },
    ];

    describe.each(cases)('on a phone, $label', ({ kindle, telegram }) => {
        it('shows the matching third button, or none', () => {
            renderCard({ isWide: false, showKindleButton: kindle, showTelegramButton: telegram });
            const names = buttonNames(actions());

            expect(names.slice(0, 2)).toEqual(['bookRead', 'bookDownload']);
            if (kindle && telegram) {
                // Both on: one «Отправить» that opens a sheet.
                expect(names).toEqual(['bookRead', 'bookDownload', 'bookSend']);
            } else if (kindle) {
                expect(names).toHaveLength(3);
                expect(names[2]).toMatch(KINDLE);
            } else if (telegram) {
                expect(names).toHaveLength(3);
                expect(names[2]).toMatch(TELEGRAM);
            } else {
                expect(names).toEqual(['bookRead', 'bookDownload']);
            }
        });
    });

    describe.each(cases)('on a wide card, $label', ({ kindle, telegram }) => {
        it('shows a send block only with something to send to', () => {
            renderCard({ isWide: true, showKindleButton: kindle, showTelegramButton: telegram });
            const block = screen.queryByTestId('book-send');

            if (!kindle && !telegram) {
                expect(block).toBeNull();
                return;
            }
            expect(block).not.toBeNull();
            const names = buttonNames(block!);
            expect(names.filter((name) => KINDLE.test(name ?? ''))).toHaveLength(kindle ? 1 : 0);
            expect(names.filter((name) => TELEGRAM.test(name ?? ''))).toHaveLength(
                telegram ? 1 : 0,
            );
            expect(names).toHaveLength(Number(kindle) + Number(telegram));
        });
    });

    it.each([
        ['Kindle', { showKindleButton: true }, 'onSendToKindle', KINDLE],
        ['Telegram', { showTelegramButton: true }, 'onSendToTelegram', TELEGRAM],
    ] as const)(
        'sends with one tap on a phone when only %s is on, with no sheet',
        async (_, shown, handler, name) => {
            const onSend = vi.fn();
            const book = makeBook();
            renderCard({ isWide: false, book, ...shown, [handler]: onSend });

            await userEvent.click(within(actions()).getByRole('button', { name }));

            expect(onSend).toHaveBeenCalledWith(book);
            expect(screen.queryByRole('dialog')).toBeNull();
        },
    );

    it('opens a sheet with both targets on a phone when both are on', async () => {
        const onSendToKindle = vi.fn();
        const book = makeBook();
        renderCard({
            isWide: false,
            book,
            showKindleButton: true,
            showTelegramButton: true,
            kindleConfigured: true,
            onSendToKindle,
        });

        await userEvent.click(within(actions()).getByRole('button', { name: 'bookSend' }));
        const sheet = await screen.findByRole('dialog');
        expect(within(sheet).getByRole('button', { name: TELEGRAM })).toBeInTheDocument();

        await userEvent.click(within(sheet).getByRole('button', { name: 'sendToKindle.send' }));
        expect(onSendToKindle).toHaveBeenCalledWith(book);
    });
});

describe('«Подробнее»', () => {
    let undo: (() => void) | undefined;
    afterEach(() => {
        undo?.();
        undo = undefined;
    });

    const more = () => screen.queryByRole('button', { name: /^(bookMore|bookLess)$/ });

    const full = makeBook({
        annotation: 'Зона.',
        publisher: 'АСТ',
        isbn: ['978-5-17-177750-0'],
        genres: [
            { id: 1, genre: 'Фантастика' },
            { id: 2, genre: 'Социальная фантастика' },
            { id: 3, genre: 'Советская классика' },
        ],
    });

    it('is absent on a wide card when nothing is hidden', () => {
        undo = fakeLayout({});
        renderCard({ isWide: true, book: full });

        expect(more()).toBeNull();
    });

    it.each([
        ['the annotation runs past three lines', { annotationClipped: true }],
        ['the genres run past one row', { chipsOnFirstRow: 2 }],
        ['the colophon is cut', { colophonClipped: true }],
    ])('is there on a wide card when %s', (_, layout) => {
        undo = fakeLayout(layout);
        renderCard({ isWide: true, book: full });

        expect(more()).toHaveAccessibleName('bookMore');
    });

    it('is there on a phone whenever the book has a colophon, which a phone hides', () => {
        undo = fakeLayout({});
        renderCard({ isWide: false, book: full });

        expect(more()).toHaveAccessibleName('bookMore');
        expect(screen.queryByTestId('book-colophon')).toBeNull();
    });

    it('is absent on a phone with no colophon and nothing clipped', () => {
        undo = fakeLayout({});
        renderCard({ isWide: false, book: makeBook({ ...full, publisher: null, isbn: [] }) });

        expect(more()).toBeNull();
    });

    it('stays to close the card once opened, and the card shows everything', async () => {
        undo = fakeLayout({ annotationClipped: true, chipsOnFirstRow: 1 });
        renderCard({ isWide: false, book: full });

        expect(screen.getByRole('button', { name: 'bookGenresMore:2' })).toHaveTextContent('+2');
        await userEvent.click(more()!);

        expect(more()).toHaveAccessibleName('bookLess');
        expect(screen.getByTestId('book-colophon')).toHaveTextContent(
            'АСТ · bookIsbn 978-5-17-177750-0',
        );
        expect(screen.queryByRole('button', { name: /^bookGenresMore/ })).toBeNull();
    });
});

describe('the byline', () => {
    const names = ['Аркадий Стругацкий', 'Борис Стругацкий', 'Кир Булычёв', 'Иван Ефремов'];
    const authors = names.map((full_name, index) => ({ id: index + 1, full_name }));
    const byline = () => within(screen.getByTestId('book-byline'));
    const shownNames = () =>
        byline()
            .queryAllByRole('link')
            .map((link) => link.textContent);

    it.each([
        [true, 2, 'bookAuthorsMore:2'],
        [false, 1, 'bookAuthorsMore:3'],
    ])('wide %s: shows %i names and counts the rest', (isWide, shown, counter) => {
        renderCard({ isWide, book: makeBook({ authors }) });

        expect(shownNames()).toEqual(names.slice(0, shown));
        expect(byline().getByRole('button', { name: counter })).toBeInTheDocument();
    });

    it.each([true, false])('shows both of two authors with no counter (wide %s)', (isWide) => {
        renderCard({ isWide, book: makeBook({ authors: authors.slice(0, 2) }) });

        expect(shownNames()).toEqual(names.slice(0, 2));
        expect(byline().queryByRole('button', { name: /^bookAuthorsMore/ })).toBeNull();
    });

    const everyLink = (root: HTMLElement) =>
        within(root)
            .getAllByRole(root.getAttribute('role') === 'menu' ? 'menuitem' : 'link')
            .map((link) => [link.textContent, link.getAttribute('href')]);

    const expected = names.map((name, index) => [name, `/author/${index + 1}`]);

    it('opens a menu of every author on a wide card', async () => {
        renderCard({ isWide: true, book: makeBook({ authors }) });

        await userEvent.click(byline().getByRole('button', { name: 'bookAuthorsMore:2' }));
        const list = await screen.findByTestId('book-authors-list');

        expect(list).toHaveAttribute('role', 'menu');
        expect(everyLink(list)).toEqual(expected);
    });

    it('opens a sheet of every author on a phone', async () => {
        renderCard({ isWide: false, book: makeBook({ authors }) });

        await userEvent.click(byline().getByRole('button', { name: 'bookAuthorsMore:3' }));
        const sheet = await screen.findByRole('dialog');

        expect(within(sheet).getByTestId('book-authors-list')).toBeInTheDocument();
        expect(everyLink(within(sheet).getByTestId('book-authors-list'))).toEqual(expected);
        await userEvent.click(within(sheet).getByRole('link', { name: 'Кир Булычёв' }));
        expect(setAuthorName).toHaveBeenCalledWith('Кир Булычёв');
    });

    const several: AuthorDisplay[] = [
        { name: 'Иван Ефремов', legacy_author_id: 4 },
        { name: 'Кир Булычёв', legacy_author_id: 3 },
        {
            name: 'Аркадий и Борис Стругацкие',
            entities: [
                { id: 'a', full_name: 'Аркадий Стругацкий' },
                { id: 'b', full_name: 'Борис Стругацкий' },
            ],
        },
    ];

    it.each([true, false])(
        'nests the people of a name of several people inside the list (wide %s)',
        async (isWide) => {
            renderCard({ isWide, book: makeBook({ authors: [], authors_display: several }) });

            await userEvent.click(byline().getByRole('button', { name: /^bookAuthorsMore/ }));
            const list = await screen.findByTestId('book-authors-list');
            const group = within(list).getByRole('group', { name: 'Аркадий и Борис Стругацкие' });
            const people = within(group)
                .getAllByRole(isWide ? 'menuitem' : 'link')
                .map((link) => [link.textContent, link.getAttribute('href')]);

            expect(people).toEqual([
                ['Аркадий Стругацкий', '/author/a'],
                ['Борис Стругацкий', '/author/b'],
            ]);
        },
    );
});

describe('the dates', () => {
    const day = 24 * 60 * 60 * 1000;
    const published = new Date(Date.now() - 400 * day);
    const pad = (value: number) => String(value).padStart(2, '0');
    const ymd = `${published.getUTCFullYear()}-${pad(published.getUTCMonth() + 1)}-${pad(published.getUTCDate())}`;
    const shown = `${pad(published.getUTCDate())}.${pad(published.getUTCMonth() + 1)}.${published.getUTCFullYear()}`;

    it.each([
        ['a bare date', ymd],
        ['a date with a time', `${ymd} 17:18`],
        ['a UTC midnight', `${ymd}T00:00:00Z`],
    ])('shows the published date without a time, given %s', (_, docdate) => {
        renderCard({ book: makeBook({ docdate }) });

        const line = screen.getByText(/^bookPublished/);
        expect(line).toHaveTextContent(`bookPublished ${shown}`);
        expect(line.textContent).not.toMatch(/\d{2}:\d{2}/);
    });

    it('leaves the added date to the list’s formatter', () => {
        const registerdate = new Date(Date.now() - 2 * day).toISOString();
        const formatDate = vi.fn(() => 'formatted-added');
        renderCard({ book: makeBook({ registerdate, docdate: ymd }), formatDate });

        expect(screen.getByText(/^bookAdded/)).toHaveTextContent('bookAdded formatted-added');
        expect(formatDate).toHaveBeenCalledWith(registerdate);
        expect(formatDate).not.toHaveBeenCalledWith(expect.stringContaining(ymd));
    });
});

describe('the star on the cover', () => {
    it.each([false, true])('toggles the favourite (in favourites: %s)', async (fav) => {
        const onToggleFavourite = vi.fn();
        const book = makeBook({ fav });
        renderCard({ book, onToggleFavourite });
        const star = screen.getByRole('button', { name: 'bookFav' });

        expect(screen.getByTestId('book-cover')).toContainElement(star);
        expect(star).toHaveAttribute('aria-pressed', String(fav));
        expect(star.className.split(/\s+/).includes('bg-amber-500')).toBe(fav);
        expect(star.querySelector('svg')?.getAttribute('class') ?? '').toSatisfy(
            (cls: string) => cls.includes('fill-current') === fav,
        );

        await userEvent.click(star);
        // The card names the file it shows too: a single book shows itself.
        expect(onToggleFavourite).toHaveBeenCalledWith(book, book.id);
    });

    it('is not in the action area', () => {
        renderCard({ isWide: false });
        expect(within(actions()).queryByRole('button', { name: 'bookFav' })).toBeNull();
    });
});

describe('the downloads', () => {
    const FORMATS = ['FB2', 'EPUB', 'MOBI', 'ZIP'];

    const handlers = () => ({
        onDownload: vi.fn(),
        onEpubRequest: vi.fn(),
        onMobiRequest: vi.fn(),
    });

    const expectRouted = (h: ReturnType<typeof handlers>, id: number) => {
        expect(h.onDownload.mock.calls).toEqual([
            ['fb2', id],
            ['zip', id],
        ]);
        expect(h.onEpubRequest).toHaveBeenCalledWith(id);
        expect(h.onMobiRequest).toHaveBeenCalledWith(id);
    };

    it('offers every format in the rail, in order, each going where it should', async () => {
        const h = handlers();
        renderCard({ isWide: true, ...h });

        const rail = actions();
        for (const format of FORMATS) {
            await userEvent.click(within(rail).getByRole('button', { name: format }));
        }
        expect(
            within(rail)
                .getAllByRole('button', { name: /^(FB2|EPUB|MOBI|ZIP)$/ })
                .map((button) => button.textContent),
        ).toEqual(FORMATS);
        expectRouted(h, 9);
    });

    it('offers every format in a sheet on a phone', async () => {
        const h = handlers();
        renderCard({ isWide: false, ...h });

        for (const format of FORMATS) {
            await userEvent.click(within(actions()).getByRole('button', { name: 'bookDownload' }));
            const sheet = await screen.findByRole('dialog');
            expect(within(sheet).getByText('bookDownload')).toBeInTheDocument();
            await userEvent.click(within(sheet).getByRole('button', { name: format }));
            // A choice closes the sheet: the conversion has its own backdrop.
            await act(async () => {});
        }
        expectRouted(h, 9);
    });

    it.each([true, false])('shows EPUB and MOBI converting (wide %s)', async (isWide) => {
        renderCard({
            isWide,
            isBookConverting: (_, format) => format === 'epub' || format === 'mobi',
        });
        let root = actions();
        if (!isWide) {
            await userEvent.click(within(root).getByRole('button', { name: 'bookDownload' }));
            root = await screen.findByRole('dialog');
            expect(within(root).getAllByText('bookConverting')).toHaveLength(2);
        }

        for (const format of ['EPUB', 'MOBI']) {
            const button = within(root).getByRole('button', { name: format });
            expect(button).toBeDisabled();
            expect(button.querySelector('[data-slot="bouncing-dots"]')).not.toBeNull();
        }
        for (const format of ['FB2', 'ZIP']) {
            expect(within(root).getByRole('button', { name: format })).toBeEnabled();
        }
    });
});

describe('the admin menu', () => {
    it.each([
        [true, 1],
        [false, 0],
    ])('is there only for a superuser (%s)', (isSuperuser, count) => {
        renderCard({ isSuperuser });
        expect(screen.queryAllByRole('button', { name: /^bookAdminActions/ })).toHaveLength(count);
    });
});

describe('a book with less to show', () => {
    it.each([true, false])('says nothing where there is no annotation (wide %s)', (isWide) => {
        // With genres, so the block an annotation would sit in is on the card.
        renderCard({
            isWide,
            book: makeBook({ annotation: '', genres: [{ id: 1, genre: 'Фантастика' }] }),
        });
        expect(screen.getByRole('link', { name: 'Фантастика' })).toBeInTheDocument();
        expect(screen.queryByText('noAnnotation')).toBeNull();
    });

    it.each([true, false])('gets a typographic tile for no cover (wide %s)', (isWide) => {
        const book: Book = makeBook({ cover: false, title: 'Литературный Сахалин, 1959' });
        renderCard({ isWide, book });

        const cover = screen.getByTestId('book-cover');
        expect(cover.querySelector('img')).toBeNull();
        const tile = within(cover).getByTestId('book-cover-tile');
        // Drawn from attributes, so the title is not in the page twice.
        expect(tile.querySelector('[data-tile-title]')).toHaveAttribute(
            'data-tile-title',
            'Литературный Сахалин, 1959',
        );
        expect(tile.querySelector('[data-tile-author]')).toHaveAttribute(
            'data-tile-author',
            'Аркадий Стругацкий',
        );
        expect(screen.getAllByText('Литературный Сахалин, 1959')).toHaveLength(1);
    });

    it('shows the picture when there is a cover', () => {
        renderCard({ book: makeBook({ cover: true }) });

        const cover = screen.getByTestId('book-cover');
        expect(cover.querySelector('img')).not.toBeNull();
        expect(within(cover).queryByTestId('book-cover-tile')).toBeNull();
    });
});
