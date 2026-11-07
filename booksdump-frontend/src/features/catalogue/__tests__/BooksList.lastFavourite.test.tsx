import React, { useSyncExternalStore } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Link, MemoryRouter, Route, Routes } from 'react-router';

import BooksList from '@/features/catalogue/BooksList';
import { FavProvider } from '@/context/FavContext';
import * as authApi from '@/api/auth';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';

// Removing the last favourite, seen through the list on screen. The real list,
// query hook, toggle and FavProvider, with the account reactive as AuthContext
// is: the transition from the last card to no cards is what is on trial, and
// neither a mocked dispatch nor an empty first answer goes through it.

const translate = (key: string, fallback?: string | object) =>
    typeof fallback === 'string' ? fallback : key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/api/books', () => ({ listBooks: vi.fn(), toggleFavourite: vi.fn() }));
vi.mock('@/api/auth', () => ({ getCurrentUser: vi.fn() }));
vi.mock('@/api/admin', () => ({ updateBook: vi.fn() }));

const account = vi.hoisted(() => {
    let user = { username: 'reader', is_superuser: false, books_lang: 'ru', have_favs: true };
    const listeners = new Set<() => void>();
    return {
        get: () => user,
        set: (next: typeof user) => {
            user = next;
            listeners.forEach((listener) => listener());
        },
        subscribe: (listener: () => void) => {
            listeners.add(listener);
            return () => listeners.delete(listener);
        },
    };
});
const setResetFavCallback = vi.fn();
vi.mock('@/context/AuthContext', () => ({
    useAuth: () => ({
        user: useSyncExternalStore(account.subscribe, account.get),
        updateUser: account.set,
        setResetFavCallback,
    }),
}));

// Stable, as the real ones are: the list's load depends on their identity.
const authorState = { authorId: '', setAuthorId: vi.fn() };
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));
const searchBarState = { searchItem: '', setSearchItem: vi.fn(), setScopeName: vi.fn() };
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchBarState }));
const conversion = { state: { convertingBooks: [], conversionErrors: [] }, dispatch: vi.fn() };
vi.mock('@/context/BookConversionContext', () => ({ useBookConversion: () => conversion }));
const socket = { isConnected: true, subscribe: vi.fn(), sendToKindle: vi.fn() };
vi.mock('@/context/WebSocketContext', () => ({ useWebSocket: () => socket }));
vi.mock('@/features/catalogue/ConversionBackdrop', () => ({ default: () => null }));

const listBooks = vi.mocked(booksApi.listBooks);
const toggleFavourite = vi.mocked(booksApi.toggleFavourite);
const getCurrentUser = vi.mocked(authApi.getCurrentUser);

const book = (id: number, title: string): Book =>
    ({
        id,
        title,
        authors: [{ id: 7, full_name: 'Автор' }],
        series: [],
        genres: [],
        annotation: '',
        filename: `book-${id}`,
        cover: false,
        registerdate: '',
        docdate: '',
        lang: 'ru',
        fav: true,
        approved: true,
        path: 'p',
        format: 'fb2',
        favorite_count: 1,
    }) as Book;

const renderFavourites = () =>
    render(
        <MemoryRouter initialEntries={['/books/favorite/1']}>
            <FavProvider>
                <Routes>
                    <Route path="/books/favorite/:page" element={<BooksList />} />
                </Routes>
            </FavProvider>
        </MemoryRouter>,
    );

/** One list element across both routes, as the application's own routes are. */
const renderBothLists = () =>
    render(
        <MemoryRouter initialEntries={['/books/favorite/1']}>
            <FavProvider>
                <Link to="/books/page/1">to-catalogue</Link>
                <Link to="/books/favorite/1">to-favourites</Link>
                <Link to="/books/favorite/2">to-next-page</Link>
                <Routes>
                    <Route path="/books/favorite/:page" element={<BooksList />} />
                    <Route path="/books/page/:page" element={<BooksList />} />
                </Routes>
            </FavProvider>
        </MemoryRouter>,
    );

const renderCatalogue = () =>
    render(
        <MemoryRouter initialEntries={['/books/page/1']}>
            <FavProvider>
                <Routes>
                    <Route path="/books/page/:page" element={<BooksList />} />
                </Routes>
            </FavProvider>
        </MemoryRouter>,
    );

const removeFirst = async () => {
    const [star] = screen.getAllByRole('button', { name: /bookFav|favou?rite/i });
    await userEvent.click(star);
};

beforeEach(() => {
    vi.clearAllMocks();
    account.set({ username: 'reader', is_superuser: false, books_lang: 'ru', have_favs: true });
    toggleFavourite.mockResolvedValue({ have_favs: true });
});

describe('Removing a favourite on the favourites list', () => {
    it('reaches the empty state when the account says none are left', async () => {
        listBooks.mockResolvedValue({ books: [book(1, 'Последняя')], length: 1 });
        getCurrentUser.mockResolvedValue({ ...account.get(), have_favs: false } as never);
        renderFavourites();
        await screen.findByText('Последняя');

        await removeFirst();

        expect(await screen.findByText('empty.favouritesTitle')).toBeInTheDocument();
        expect(screen.queryByText('Последняя')).toBeNull();
        // Nothing was asked for: the answer is already known.
        expect(listBooks).toHaveBeenCalledTimes(1);
    });

    it('goes by the list on screen at the click, after a turn of the pages too', async () => {
        listBooks.mockResolvedValue({ books: [book(1, 'Последняя')], length: 2 });
        getCurrentUser.mockResolvedValue({ ...account.get(), have_favs: false } as never);
        renderBothLists();
        await screen.findByText('Последняя');
        await userEvent.click(screen.getByRole('link', { name: 'to-next-page' }));
        await waitFor(() => expect(listBooks).toHaveBeenCalledTimes(2));
        await screen.findByText('Последняя');

        await removeFirst();

        expect(await screen.findByText('empty.favouritesTitle')).toBeInTheDocument();
    });

    it('keeps the list as it is while any remain', async () => {
        listBooks.mockResolvedValue({
            books: [book(1, 'Первая'), book(2, 'Вторая')],
            length: 1,
        });
        getCurrentUser.mockResolvedValue({ ...account.get(), have_favs: true } as never);
        renderFavourites();
        await screen.findByText('Первая');

        await removeFirst();

        await waitFor(() => expect(getCurrentUser).toHaveBeenCalled());
        await waitFor(() => expect(toggleFavourite).toHaveBeenCalled());
        expect(screen.getByText('Первая')).toBeInTheDocument();
        expect(screen.getByText('Вторая')).toBeInTheDocument();
        expect(screen.queryByText('empty.favouritesTitle')).toBeNull();
    });

    it('leaves any other list alone when the last favourite goes from there', async () => {
        listBooks.mockResolvedValue({ books: [book(1, 'В каталоге')], length: 1 });
        getCurrentUser.mockResolvedValue({ ...account.get(), have_favs: false } as never);
        renderCatalogue();
        await screen.findByText('В каталоге');

        await removeFirst();

        await waitFor(() => expect(account.get().have_favs).toBe(false));
        expect(screen.getByText('В каталоге')).toBeInTheDocument();
    });

    describe('when the reader has left before the answer comes', () => {
        const held = () => {
            let finish: (value: never) => void = () => {};
            toggleFavourite.mockReturnValue(
                new Promise((resolve) => {
                    finish = resolve as (value: never) => void;
                }),
            );
            return (value: unknown) => finish(value as never);
        };

        beforeEach(() => {
            getCurrentUser.mockResolvedValue({ ...account.get(), have_favs: false } as never);
            listBooks.mockImplementation(async ({ fav }) =>
                fav
                    ? { books: [book(1, 'Последняя')], length: 1 }
                    : { books: [book(2, 'Из каталога')], length: 1 },
            );
        });

        it('leaves the list they went to alone', async () => {
            const finish = held();
            renderBothLists();
            await screen.findByText('Последняя');

            await removeFirst();
            await userEvent.click(screen.getByRole('link', { name: 'to-catalogue' }));
            await screen.findByText('Из каталога');

            await act(async () => finish({ have_favs: false }));

            await waitFor(() => expect(account.get().have_favs).toBe(false));
            expect(screen.getByText('Из каталога')).toBeInTheDocument();
            expect(screen.queryByText('noBooksFound')).toBeNull();
        });

        it('leaves the catalogue alone on the way back to favourites too', async () => {
            // A list of its own again, loaded after the removal: the account now
            // says none are left, and that list answers for itself.
            const finish = held();
            renderBothLists();
            await screen.findByText('Последняя');

            await removeFirst();
            await userEvent.click(screen.getByRole('link', { name: 'to-catalogue' }));
            await screen.findByText('Из каталога');
            await userEvent.click(screen.getByRole('link', { name: 'to-favourites' }));
            await screen.findByText('Последняя');
            const requests = listBooks.mock.calls.length;

            await act(async () => finish({ have_favs: false }));
            await waitFor(() => expect(account.get().have_favs).toBe(false));

            // Another visit to the route is another list, so nothing is emptied by
            // an answer from before it; no request is added either.
            expect(listBooks).toHaveBeenCalledTimes(requests);
            expect(screen.getByText('Последняя')).toBeInTheDocument();
        });
    });

    it('does not empty the list when the request fails', async () => {
        listBooks.mockResolvedValue({ books: [book(1, 'Последняя')], length: 1 });
        toggleFavourite.mockRejectedValue(new Error('down'));
        renderFavourites();
        await screen.findByText('Последняя');

        await removeFirst();

        await waitFor(() => expect(toggleFavourite).toHaveBeenCalled());
        expect(screen.getByText('Последняя')).toBeInTheDocument();
        expect(screen.queryByText('empty.favouritesTitle')).toBeNull();
    });
});
