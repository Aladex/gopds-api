import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';
import { fakeLayout, translate } from './bookCardHarness';

/*
 * The card shows the book the way a list is scanned — title, byline, series,
 * dates, genres, a colophon and the start of the annotation — and opens to
 * show the rest. Opening is one real button, «Подробнее», and it is offered
 * only when the card is holding something back (BookCard.redesign holds the
 * rule itself); here the annotation is made to run long so it is there.
 */

const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const setSearchItem = vi.fn();
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem }) }));

const setAuthorName = vi.fn();
vi.mock('@/context/AuthorContext', () => ({
    useAuthor: () => ({ setAuthorName }),
}));

const AUTHORS = [
    'Аркадий Стругацкий',
    'Борис Стругацкий',
    'Кир Булычёв',
    'Иван Ефремов',
    'Роберт Хайнлайн',
];

const book: Book = {
    id: 1,
    title: 'Полдень, XXII век',
    authors: AUTHORS.map((full_name, index) => ({ id: index + 1, full_name })),
    series: [{ id: 7, ser: 'Мир Полудня', ser_no: 1 }],
    genres: [
        { id: 11, genre: 'Научная фантастика' },
        { id: 12, genre: 'Социальная фантастика' },
        { id: 13, genre: 'Советская классика' },
    ],
    annotation: 'Возвращение.',
    filename: 'noon',
    cover: false,
    registerdate: '2020-01-01',
    docdate: '1962-01-01',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/noon',
    format: 'fb2',
    favorite_count: 0,
};

const renderCard = ({ isWide = true } = {}) =>
    render(
        <MemoryRouter>
            <BookCard
                book={book}
                isWide={isWide}
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

/** The one control that opens and shuts the card. */
const disclosure = () => screen.getByRole('button', { name: /bookMore|bookLess/ });

let undoLayout: () => void;
beforeEach(() => {
    vi.clearAllMocks();
    undoLayout = fakeLayout({ annotationClipped: true });
});
afterEach(() => undoLayout());

describe('a card that is shut', () => {
    // The title is what the list is scanned by, so it is the one thing that is
    // never shortened.
    it('shows the title in full', () => {
        renderCard();

        const title = screen.getByRole('heading', { name: book.title });
        expect(title.className).not.toMatch(/line-clamp|truncate/);
    });

    it('clamps the annotation to three lines at both sizes', () => {
        for (const isWide of [true, false]) {
            const view = renderCard({ isWide });
            expect(screen.getByText('Возвращение.').className).toMatch(/\bline-clamp-3\b/);
            view.unmount();
        }
    });
});

describe('a card that has been opened', () => {
    it('lets the annotation and the genres run in full', async () => {
        const { container } = renderCard();

        await userEvent.click(disclosure());

        expect(screen.getByText('Возвращение.').className).not.toMatch(/line-clamp/);
        expect(container.querySelector('[data-clip="chips"]')?.className).not.toMatch(/max-h-/);
    });
});

/*
 * There is exactly one way to open a card, it is a real button, and it says
 * which way it is about to go. The card used to open on a click anywhere on
 * it — undiscoverable, unreachable from a keyboard, and prone to shutting
 * itself the moment anyone selected a line of the annotation with the mouse.
 */
describe('opening and shutting a card', () => {
    it('is done by one control that says what it does', async () => {
        renderCard();

        const control = disclosure();
        expect(control).toHaveAccessibleName('bookMore');
        expect(control).toHaveAttribute('aria-expanded', 'false');

        await userEvent.click(control);
        expect(disclosure()).toHaveAccessibleName('bookLess');
        expect(disclosure()).toHaveAttribute('aria-expanded', 'true');

        await userEvent.click(disclosure());
        expect(disclosure()).toHaveAccessibleName('bookMore');
    });

    it('names the block it opens', () => {
        const { container } = renderCard();

        const controlled = disclosure().getAttribute('aria-controls');
        expect(controlled).toBeTruthy();
        expect(container.querySelector(`#${controlled}`)).toContainElement(
            screen.getByText('Возвращение.'),
        );
    });

    it('can be reached and worked from the keyboard', async () => {
        renderCard();

        disclosure().focus();
        expect(disclosure()).toHaveFocus();

        await userEvent.keyboard('{Enter}');
        expect(screen.getByTestId('book-card')).toHaveAttribute('data-state', 'open');
    });

    // Nothing else on the card opens it, so selecting a line of the annotation
    // or missing a link by a few pixels costs nothing.
    it('is not opened by pressing the card itself', async () => {
        renderCard();

        const card = screen.getByTestId('book-card');
        await userEvent.click(screen.getByRole('heading', { name: book.title }));
        expect(card).toHaveAttribute('data-state', 'collapsed');

        await userEvent.click(card);
        expect(card).toHaveAttribute('data-state', 'collapsed');
    });
});

describe('the values on the card', () => {
    /*
     * Links rather than buttons: these are navigations, and as links they can
     * be opened in a new tab, which a button dispatching navigate() never can.
     */
    it('are links to where they go', () => {
        renderCard();

        expect(screen.getByRole('link', { name: 'Аркадий Стругацкий' })).toHaveAttribute(
            'href',
            '/books/find/author/1/1',
        );
        expect(screen.getByRole('link', { name: 'Мир Полудня' })).toHaveAttribute(
            'href',
            '/books/find/category/7/1',
        );
        expect(screen.getByRole('link', { name: 'Советская классика' })).toHaveAttribute(
            'href',
            '/books/find/genre/13/1',
        );
    });

    it('drop the scope the reader was browsing under on the way', async () => {
        renderCard();

        await userEvent.click(screen.getByRole('link', { name: 'Борис Стругацкий' }));

        // A stale query would otherwise survive the move and filter the author's
        // own books. The name is handed over rather than fetched again: it is on
        // screen already.
        expect(setSearchItem).toHaveBeenCalledWith('');
        expect(setAuthorName).toHaveBeenCalledWith('Борис Стругацкий');
    });

    it('name the series with its label, as one line', () => {
        renderCard();

        const line = screen.getByRole('link', { name: 'Мир Полудня' }).parentElement!;
        expect(line).toHaveTextContent('bookSeriesLabel: Мир Полудня');
    });
});
