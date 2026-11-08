import React from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';

import BooksList from '@/features/catalogue/BooksList';
import * as booksApi from '@/api/books';
import * as authApi from '@/api/auth';
import type { Book, BooksPage } from '@/api/books';

/*
 * The site asks for cards on the scoped lists only: an author, a series, a
 * genre, a curated collection, the reader's favourites and everyone's. The
 * feed and the search stay one row per file. A card's star is the card's:
 * unstarring it unstars every file of it.
 */

// A stable t: useTranslation must not hand back a fresh function each render,
// or effects keyed on it loop forever.
const translate = (key: string, fallback?: string | object) =>
    typeof fallback === 'string' ? fallback : key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/api/books', () => ({
    listBooks: vi.fn(),
    toggleFavourite: vi.fn(),
    getBookVariants: vi.fn(),
}));
vi.mock('@/api/auth', () => ({ getCurrentUser: vi.fn() }));
vi.mock('@/api/admin', () => ({ updateBook: vi.fn() }));

const authState = {
    user: {
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        books_lang: 'ru',
    },
    updateLang: vi.fn(),
    updateUser: vi.fn(),
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

const favState = { fav: false, favEnabled: true, setFavEnabled: vi.fn() };
vi.mock('@/context/FavContext', () => ({ useFav: () => favState }));

const authorState = {
    authorId: '',
    setAuthorId: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

// The card clears the search box when a reader clicks through to a filtered
// list, so the filter applies rather than a stale query.
const searchBarState = {
    searchItem: '',
    setSearchItem: vi.fn(),
    languages: ['ru'],
    selectedLanguage: 'ru',
    setSelectedLanguage: vi.fn(),
    scopeName: '',
    setScopeName: vi.fn(),
};
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchBarState }));

// The real hook returns { state, dispatch }; BooksList derives
// isBookConverting itself from state.convertingBooks.
const conversionState: {
    convertingBooks: { bookID: number; format: string }[];
    conversionErrors: { bookID: number; format: string; message: string }[];
} = { convertingBooks: [], conversionErrors: [] };
const conversionValue = { state: conversionState, dispatch: vi.fn() };
vi.mock('@/context/BookConversionContext', () => ({
    useBookConversion: () => conversionValue,
}));

vi.mock('@/shared/lib/downloadViaIframe', () => ({ downloadViaIframe: vi.fn() }));

vi.mock('@/shared/layout/landOnList', async () => ({
    ...(await vi.importActual<typeof import('@/shared/layout/landOnList')>(
        '@/shared/layout/landOnList',
    )),
    scrollToLoadedList: vi.fn(),
}));

// The cards' Kindle button asks the socket; nothing here sends a book.
const webSocketValue = { isConnected: true, subscribe: vi.fn(), sendToKindle: vi.fn() };
vi.mock('@/context/WebSocketContext', () => ({ useWebSocket: () => webSocketValue }));

// The preview client is mocked at the boundary the dialog uses, so the tests
// below exercise the real dialog: whether the catalogue can reach it at all
// is precisely what they are here to hold.
vi.mock('@/api/preview', async () => {
    const actual = await vi.importActual<typeof import('@/api/preview')>('@/api/preview');
    return {
        ...actual,
        previewClient: { getPreview: vi.fn(), getChunk: vi.fn(), getImage: vi.fn() },
    };
});

// The conversion backdrop is a full-screen MUI modal. It is a separate concern,
// and leaving it in hides the card behind an overlay whenever a conversion is
// in flight.
vi.mock('@/features/catalogue/ConversionBackdrop', () => ({ default: () => null }));

const listBooks = vi.mocked(booksApi.listBooks);
const toggleFavourite = vi.mocked(booksApi.toggleFavourite);
const getBookVariants = vi.mocked(booksApi.getBookVariants);
const getCurrentUser = vi.mocked(authApi.getCurrentUser);

function makeBook(over: Partial<Book> = {}): Book {
    return {
        id: 1,
        title: 'Заклятые в любви',
        authors: [{ id: 7, full_name: 'Райнер Анастасия' }],
        series: [],
        genres: [{ id: 3, genre: 'love_contemporary' }],
        annotation: 'Атмосфера студенческой жизни и уют кампуса.',
        filename: 'book-1',
        cover: true,
        registerdate: '2026-07-07T20:18:00Z',
        docdate: '2024-08-26',
        lang: 'ru',
        fav: false,
        approved: true,
        path: 'fb2-1-2.zip',
        format: 'fb2',
        favorite_count: 0,
        ...over,
    };
}

function renderAt(path: string, route = '/books/page/:page') {
    return render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path={route} element={<BooksList />} />
                <Route path="*" element={<BooksList />} />
            </Routes>
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    authState.user.is_superuser = false;
    conversionState.convertingBooks = [];
    conversionState.conversionErrors = [];
    listBooks.mockResolvedValue({ books: [makeBook()], length: 1 });
    toggleFavourite.mockResolvedValue({ have_favs: true });
    getCurrentUser.mockResolvedValue({
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        have_favs: true,
    });
});

const scoped = [
    ['an author', '/author/42', '/author/:id'],
    ['a series', '/books/find/category/9/1', '/books/find/category/:id/:page'],
    ['a genre', '/books/find/genre/5/1', '/books/find/genre/:id/:page'],
    ['a curated collection', '/collections/3/page/1', '/collections/:id/page/:page'],
    ['the favourites', '/books/favorite/1', '/books/favorite/:page'],
    ['the feed', '/books/page/1', '/books/page/:page'],
    ["everyone's favourites", '/books/users/favorites/1', '/books/users/favorites/:page'],
] as const;

describe('which lists ask for cards', () => {
    it.each(scoped)('sends cards=1 on %s', async (_name, path, route) => {
        renderAt(path, route);
        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ cards: true }));
    });

    it.each([
        ['a book search', `/search?q=${encodeURIComponent('дюна')}&type=books`, '/search'],
        [
            'a search inside an author',
            `/author/42?title=${encodeURIComponent('война')}`,
            '/author/:id',
        ],
        ['a pinned book', '/author/42?book_id=7', '/author/:id'],
    ])('sends no cards on %s', async (_name, path, route) => {
        renderAt(path, route);
        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks.mock.calls[0][0]).not.toHaveProperty('cards');
    });
});

beforeAll(() => {
    // vaul's sheet captures the pointer on press; jsdom has no such API.
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
});

describe("a card's star", () => {
    const variants = { key: 'card-key', count: 3, kind: 'variants' as const };
    const star = () => screen.getByRole('button', { name: 'bookFav' });

    it('unstar sends whole_group', async () => {
        listBooks.mockResolvedValue({
            books: [makeBook({ id: 10, fav: true, variants })],
            length: 1,
        } as BooksPage);
        renderAt('/author/42', '/author/:id');

        await userEvent.click(await screen.findByRole('button', { name: 'bookFav' }));
        await waitFor(() => expect(toggleFavourite).toHaveBeenCalledWith(10, false, true));
        expect(star()).toHaveAttribute('aria-pressed', 'false');
    });

    it('stars the shown file alone', async () => {
        listBooks.mockResolvedValue({
            books: [makeBook({ id: 10, fav: false, variants })],
            length: 1,
        } as BooksPage);
        renderAt('/author/42', '/author/:id');

        await userEvent.click(await screen.findByRole('button', { name: 'bookFav' }));
        await waitFor(() => expect(toggleFavourite).toHaveBeenCalledWith(10, true));
        expect(star()).toHaveAttribute('aria-pressed', 'true');
    });

    it('stars the file the card shows after a pick', async () => {
        listBooks.mockResolvedValue({
            books: [makeBook({ id: 10, fav: false, variants })],
            length: 1,
        } as BooksPage);
        getBookVariants.mockResolvedValue({
            key: 'card-key',
            kind: 'variants',
            count: 2,
            default_id: 10,
            variants: [
                {
                    ...makeBook({ id: 10 }),
                    lineage: 'a',
                    latest: true,
                    rank: 1,
                    tag: '',
                    doc_version: '1',
                    default: true,
                    translators: ['Анна Первая'],
                },
                {
                    ...makeBook({ id: 20 }),
                    lineage: 'b',
                    latest: true,
                    rank: 2,
                    tag: '',
                    doc_version: '1',
                    default: false,
                    translators: ['Борис Второй'],
                },
            ],
        });
        renderAt('/author/42', '/author/:id');

        // A phone here (jsdom matches no media query): the chip and its sheet.
        await userEvent.click(await screen.findByTestId('variant-chip'));
        const sheet = await screen.findByRole('dialog');
        // The second row is the second file (this file's translator stub has no interpolation).
        const rows = await within(sheet).findAllByRole('listitem');
        await userEvent.click(within(rows[1]).getByRole('button'));
        await waitFor(() =>
            expect(screen.getByTestId('variant-chip')).toHaveTextContent('variantOf'),
        );

        await userEvent.click(star());
        await waitFor(() => expect(toggleFavourite).toHaveBeenCalledWith(20, true));
    });

    it('unstars a single book as before', async () => {
        listBooks.mockResolvedValue({
            books: [makeBook({ id: 1, fav: true })],
            length: 1,
        } as BooksPage);
        renderAt('/author/42', '/author/:id');

        await userEvent.click(await screen.findByRole('button', { name: 'bookFav' }));
        await waitFor(() => expect(toggleFavourite).toHaveBeenCalledWith(1, false));
        expect(star()).toHaveAttribute('aria-pressed', 'false');
    });

    it('flips the card by its key, and only that card', async () => {
        listBooks.mockResolvedValue({
            books: [
                makeBook({ id: 10, fav: true, variants }),
                makeBook({ id: 11, fav: true, title: 'Другая' }),
            ],
            length: 1,
        } as BooksPage);
        renderAt('/author/42', '/author/:id');

        const stars = await screen.findAllByRole('button', { name: 'bookFav' });
        await userEvent.click(stars[0]);
        await waitFor(() => expect(toggleFavourite).toHaveBeenCalled());
        const after = screen.getAllByRole('button', { name: 'bookFav' });
        expect(after[0]).toHaveAttribute('aria-pressed', 'false');
        expect(after[1]).toHaveAttribute('aria-pressed', 'true');
    });
});
