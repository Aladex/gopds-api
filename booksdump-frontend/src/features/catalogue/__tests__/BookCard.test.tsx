import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

/*
 * The card holds three lists — authors, series, genres — and while it is shut
 * each of them shows one line. What does the shortening is `-webkit-line-clamp`,
 * which jsdom does not implement and could not honour anyway without a
 * stylesheet or a layout. So how it looks was measured in a browser: five
 * authors in a 520px row cut after the third with the ellipsis after a whole
 * name, no ellipsis at all where everything fitted, and — the case that sent
 * this back for a second try — a single series longer than a 190px row showing
 * as much of itself as fits rather than disappearing and leaving the label over
 * an ellipsis.
 *
 * What is held here is the half that survives without layout, and the half the
 * browser cannot check: that nothing is removed from the document to make it
 * fit. Slicing the lists in JavaScript — which is what the authors used to get,
 * two of them and a count of the rest — takes the names away from anyone
 * reading the page with something other than their eyes.
 */

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const setSearchItem = vi.fn();
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem }) }));

const setAuthorName = vi.fn();
const clearAuthorBook = vi.fn();
vi.mock('@/context/AuthorContext', () => ({
    useAuthor: () => ({ setAuthorName, clearAuthorBook }),
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

const renderCard = () =>
    render(
        <MemoryRouter>
            <BookCard
                book={book}
                annotationPeekLines={2}
                showLanguage={false}
                isSuperuser={false}
                formatDate={(value) => value}
                isBookConverting={() => false}
                onDownload={vi.fn()}
                onEpubRequest={vi.fn()}
                onMobiRequest={vi.fn()}
                onToggleFavourite={vi.fn()}
                onToggleApproved={vi.fn()}
                onRescan={vi.fn()}
                onEdit={vi.fn()}
            />
        </MemoryRouter>,
    );

/** The row a given value sits in, whichever of the three lists that is. */
const rowOf = (value: string) => screen.getByRole('link', { name: value }).closest('div');

beforeEach(() => vi.clearAllMocks());

describe('a card that is shut', () => {
    it('keeps every author in the document, however many there are', async () => {
        renderCard();

        // Including the ones the clamp will hide: the list is shortened by the
        // browser, so what a reader with a screen reader hears is all of it.
        for (const name of AUTHORS) {
            expect(await screen.findByRole('link', { name })).toBeInTheDocument();
        }
    });

    it('clamps each of the three lists to a line', () => {
        renderCard();

        expect(rowOf('Аркадий Стругацкий')?.className).toMatch(/line-clamp-1/);
        expect(rowOf('Мир Полудня')?.className).toMatch(/line-clamp-1/);
        expect(rowOf('Научная фантастика')?.className).toMatch(/line-clamp-1/);
    });

    // The title is what the list is scanned by, so it is the one thing that is
    // never shortened.
    it('shows the title in full', () => {
        renderCard();

        const title = screen.getByRole('heading', { name: book.title });
        expect(title.className).not.toMatch(/line-clamp|truncate/);
    });
});

describe('a card that has been opened', () => {
    it('lets all three lists wrap', async () => {
        renderCard();

        await userEvent.click(screen.getByRole('heading', { name: book.title }));

        expect(rowOf('Аркадий Стругацкий')?.className).not.toMatch(/line-clamp-1/);
        expect(rowOf('Мир Полудня')?.className).not.toMatch(/line-clamp-1/);
        expect(rowOf('Научная фантастика')?.className).not.toMatch(/line-clamp-1/);
    });
});

describe('the values in those lists', () => {
    /*
     * Links rather than buttons, for two reasons that happen to agree. A button
     * is an atomic inline box whatever its display, so one longer than the row
     * vanished whole and left the label over an ellipsis. And these are
     * navigations: as links they can be opened in a new tab, which a button
     * dispatching navigate() can never be.
     */
    it('are links to where they go', () => {
        renderCard();

        expect(screen.getByRole('link', { name: 'Кир Булычёв' })).toHaveAttribute(
            'href',
            '/books/find/author/3/1',
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

        await userEvent.click(screen.getByRole('link', { name: 'Кир Булычёв' }));

        // A stale query would otherwise survive the move and filter the author's
        // own books. The name is handed over rather than fetched again: it is on
        // screen already.
        expect(setSearchItem).toHaveBeenCalledWith('');
        expect(clearAuthorBook).toHaveBeenCalled();
        expect(setAuthorName).toHaveBeenCalledWith('Кир Булычёв');
    });

    // Following one of these navigates; it must not also toggle the card it sits
    // in, which the card's own click handler would otherwise do on the way up.
    it('does not open the card on the way past', async () => {
        renderCard();

        const card = screen.getByTestId('book-card');
        expect(card).toHaveAttribute('data-state', 'collapsed');

        await userEvent.click(screen.getByRole('link', { name: 'Кир Булычёв' }));
        expect(card).toHaveAttribute('data-state', 'collapsed');
    });
});
