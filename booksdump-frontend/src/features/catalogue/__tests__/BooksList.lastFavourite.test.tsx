import React, { useSyncExternalStore } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';

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
